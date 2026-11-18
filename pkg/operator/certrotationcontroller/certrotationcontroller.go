package certrotationcontroller

import (
	"context"
	"fmt"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	features "github.com/openshift/api/features"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisterv1 "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	certrotationv2 "github.com/openshift/library-go/pkg/operator/pki/certrotation"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/openshift/library-go/pkg/pki"
)

type CertRotationController struct {
	// certRotators are the v1 controllers. They serve RunOnce (which relies
	// on the v1 RunOnceContextKey semantics) in addition to Run.
	certRotators []factory.Controller
	// v2Rotators are the certrotation v2 controllers, used when the
	// ConfigurablePKI feature gate is enabled. Controller-only for now.
	v2Rotators []factory.Controller

	networkLister        configlisterv1.NetworkLister
	infrastructureLister configlisterv1.InfrastructureLister

	serviceNetwork        *DynamicServingRotation
	serviceHostnamesQueue workqueue.RateLimitingInterface

	externalLoadBalancer               *DynamicServingRotation
	externalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	internalLoadBalancer               *DynamicServingRotation
	internalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	recorder events.Recorder

	cachesToSync []cache.InformerSynced
}

func NewCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGates featuregates.FeatureGate,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGates,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGates featuregates.FeatureGate,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGates,
		true,
	)
}

func newCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGates featuregates.FeatureGate,
	refreshOnlyWhenExpired bool,
) (*CertRotationController, error) {
	ret := &CertRotationController{
		networkLister:        configInformer.Config().V1().Networks().Lister(),
		infrastructureLister: configInformer.Config().V1().Infrastructures().Lister(),

		serviceHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ServiceHostnames"),
		serviceNetwork:        &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		externalLoadBalancerHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ExternalLoadBalancerHostnames"),
		externalLoadBalancer:               &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		internalLoadBalancerHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "InternalLoadBalancerHostnames"),
		internalLoadBalancer:               &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		recorder: eventRecorder,
		cachesToSync: []cache.InformerSynced{
			configInformer.Config().V1().Networks().Informer().HasSynced,
			configInformer.Config().V1().Infrastructures().Informer().HasSynced,
		},
	}

	configInformer.Config().V1().Networks().Informer().AddEventHandler(ret.serviceHostnameEventHandler())
	configInformer.Config().V1().Infrastructures().Informer().AddEventHandler(ret.externalLoadBalancerHostnameEventHandler())

	foreverPeriod := 10 * 365 * 24 * time.Hour
	foreverRefreshPeriod := 8 * 365 * 24 * time.Hour

	rotationDay := 24 * time.Hour
	// for the development cycle, make the rotation 60 times faster (every twelve hours or so).
	// This must be reverted before we ship
	rotationDay = rotationDay / 60

	// Some certificates should not be affected by development cycle rotation
	devRotationExceptionDay := 24 * time.Hour

	monthPeriod := 30 * rotationDay
	devRotationExceptionMonth := 30 * devRotationExceptionDay
	yearPeriod := 365 * rotationDay
	devRotationExceptionYear := 365 * devRotationExceptionDay
	tenMonthPeriod := 292 * rotationDay
	devRotationExceptionTenMonth := 292 * devRotationExceptionDay

	// Set custom rotation duration when FeatureShortCertRotation is enabled
	if featureGates.Enabled(features.FeatureShortCertRotation) {
		monthPeriod = 2 * time.Hour
		devRotationExceptionMonth = monthPeriod
		yearPeriod = 3 * time.Hour
		devRotationExceptionYear = yearPeriod
		tenMonthPeriod = 150 * time.Minute
		devRotationExceptionTenMonth = tenMonthPeriod
	}
	klog.Infof("Setting monthPeriod to %v, yearPeriod to %v, tenMonthPeriod to %v", monthPeriod, yearPeriod, tenMonthPeriod)

	periods := rotationPeriods{
		monthPeriod:                  monthPeriod,
		devRotationExceptionMonth:    devRotationExceptionMonth,
		yearPeriod:                   yearPeriod,
		devRotationExceptionYear:     devRotationExceptionYear,
		tenMonthPeriod:               tenMonthPeriod,
		devRotationExceptionTenMonth: devRotationExceptionTenMonth,
		foreverPeriod:                foreverPeriod,
		foreverRefreshPeriod:         foreverRefreshPeriod,
	}
	specs := rotationSpecs(periods, dynamicHostnameSources{
		serviceNetwork:       ret.serviceNetwork,
		externalLoadBalancer: ret.externalLoadBalancer,
		internalLoadBalancer: ret.internalLoadBalancer,
	})

	var pkiConfigProvider pki.ConfigProvider
	if featureGates != nil && featureGates.Enabled(features.FeatureGateConfigurablePKI) {
		ret.cachesToSync = append(ret.cachesToSync, configInformer.Config().V1alpha1().PKIs().Informer().HasSynced)
		pkiConfigProvider = pki.NewClusterConfigProvider(configInformer.Config().V1alpha1().PKIs().Lister())

		// v2 path: group the specs by signer into Rotations and build one v2
		// controller per Rotation. The writer owns the full artifact
		// lifecycle; the refreshOnlyWhenExpired variant (the
		// cert-regeneration-controller twin) runs as a healer.
		role := certrotationv2.RoleWriter
		if refreshOnlyWhenExpired {
			role = certrotationv2.RoleHealer
		}
		rotations, err := toV2Rotations(specs)
		if err != nil {
			return nil, err
		}
		reporter := certrotationv2.NewStaticPodConditionReporter(operatorClient)
		for i := range rotations {
			controller, err := certrotationv2.NewController(rotations[i], certrotationv2.Options{
				Role:       role,
				KubeClient: kubeClient,
				PKI:        certrotationv2.ConfigurablePKI(pkiConfigProvider),
				Recorder:   eventRecorder,
				Informers:  kubeInformersForNamespaces,
				Reporter:   reporter,
			})
			if err != nil {
				return nil, err
			}
			ret.v2Rotators = append(ret.v2Rotators, controller)
		}
		return ret, nil
	}

	// v1 path: unchanged behavior, one controller per spec.
	v1Controllers, err := toV1Controllers(
		specs,
		v1ControllerDeps{
			kubeClient:                 kubeClient,
			kubeInformersForNamespaces: kubeInformersForNamespaces,
			eventRecorder:              eventRecorder,
			reporter:                   &certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
		},
		refreshOnlyWhenExpired,
		pkiConfigProvider,
	)
	if err != nil {
		return nil, err
	}
	ret.certRotators = append(ret.certRotators, v1Controllers...)

	return ret, nil
}

func (c *CertRotationController) WaitForReady(stopCh <-chan struct{}) {
	klog.Infof("Waiting for CertRotation")
	defer klog.Infof("Finished waiting for CertRotation")

	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// need to sync at least once before beginning.  if we fail, we cannot start rotating certificates
	if err := c.syncServiceHostnames(); err != nil {
		panic(err)
	}
	if err := c.syncExternalLoadBalancerHostnames(); err != nil {
		panic(err)
	}
	if err := c.syncInternalLoadBalancerHostnames(); err != nil {
		panic(err)
	}
}

// RunOnce will run the cert rotation logic, but will not try to update the static pod status.
// This eliminates the need to pass an OperatorClient and avoids dubious writes and status.
func (c *CertRotationController) RunOnce() error {
	errlist := []error{}
	runOnceCtx := context.WithValue(context.Background(), certrotation.RunOnceContextKey, true)
	for _, certRotator := range c.certRotators {
		if err := certRotator.Sync(runOnceCtx, factory.NewSyncContext("CertRotationController", c.recorder)); err != nil {
			errlist = append(errlist, err)
		}
	}

	return utilerrors.NewAggregate(errlist)
}

func (c *CertRotationController) Run(ctx context.Context, workers int) {
	klog.Infof("Starting CertRotation")
	defer klog.Infof("Shutting down CertRotation")
	c.WaitForReady(ctx.Done())

	go wait.Until(c.runServiceHostnames, time.Second, ctx.Done())
	go wait.Until(c.runExternalLoadBalancerHostnames, time.Second, ctx.Done())
	go wait.Until(c.runInternalLoadBalancerHostnames, time.Second, ctx.Done())

	for _, certRotator := range c.certRotators {
		go certRotator.Run(ctx, workers)
	}
	for _, certRotator := range c.v2Rotators {
		go certRotator.Run(ctx, workers)
	}

	if len(c.v2Rotators) > 0 {
		// On the v2 path no v1 controller consumes the
		// DynamicServingRotation hostnamesChanged channels; drain them so
		// setHostnames never blocks once the channel buffers fill. The v2
		// controllers read the hostnames on their resync instead.
		go drainHostnamesChanges(c.serviceNetwork.hostnamesChanged)
		go drainHostnamesChanges(c.externalLoadBalancer.hostnamesChanged)
		go drainHostnamesChanges(c.internalLoadBalancer.hostnamesChanged)
	}

	<-ctx.Done()
}

func drainHostnamesChanges(ch <-chan struct{}) {
	for range ch {
	}
}
