package certrotationcontroller

import (
	"fmt"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisterv1 "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/cluster-kube-apiserver-operator/tls"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

type CertRotationController struct {
	certRotators map[string]*certrotation.CertRotationController

	networkLister        configlisterv1.NetworkLister
	infrastructureLister configlisterv1.InfrastructureLister

	serviceNetwork        *DynamicServingRotation
	serviceHostnamesQueue workqueue.RateLimitingInterface

	externalLoadBalancer               *DynamicServingRotation
	externalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	internalLoadBalancer               *DynamicServingRotation
	internalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	cachesToSync []cache.InformerSynced
}

func NewCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
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

		cachesToSync: []cache.InformerSynced{
			configInformer.Config().V1().Networks().Informer().HasSynced,
			configInformer.Config().V1().Infrastructures().Informer().HasSynced,
		},
	}

	configInformer.Config().V1().Networks().Informer().AddEventHandler(ret.serviceHostnameEventHandler())
	configInformer.Config().V1().Infrastructures().Informer().AddEventHandler(ret.externalLoadBalancerHostnameEventHandler())

	rotationDay := defaultRotationDay
	if day != time.Duration(0) {
		rotationDay = day
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", rotationDay)
	}

	for name, rc := range map[string]tls.RotatedCertificate{
		"AggregatorProxyClientCert":        tls.OpenShiftKubeAPIServer_AggregatorClient,
		"KubeAPIServerToKubeletClientCert": tls.OpenShiftKubeAPIServer_KubeletClient,
		"LocalhostServing":                 tls.OpenShiftKubeAPIServer_LocalhostServingCertCertKey,
		"ServiceNetworkServing":            tls.OpenShiftKubeAPIServer_ServingNetworkServingCertKey,
		"ExternalLoadBalancerServing":      tls.OpenShiftKubeAPIServer_ExternalLoadBalancerServingCertKey,
		"InternalLoadBalancerServing":      tls.OpenShiftKubeAPIServer_InternalLoadBalancerServingCertKey,
		"KubeControllerManagerClient":      tls.OpenShiftConfigManaged_KubeControllerManagerClientCertKey,
		"KubeSchedulerClient":              tls.OpenShiftConfigManaged_KubeSchedulerClientCertKey,
		"KubeAPIServerCertSyncer":          tls.OpenShiftConfigManaged_KubeAPIServerCertSyncerClientCertKey,
	} {
		rotator, err := certrotation.NewCertRotationController(
			name,
			certrotation.SigningRotation{
				Namespace:     rc.Signer.Namespace,
				Name:          rc.Signer.Name,
				Validity:      rc.Signer.Validity,
				Refresh:       rc.Signer.Refresh,
				Informer:      kubeInformersForNamespaces.InformersFor(rc.Signer.Namespace).Core().V1().Secrets(),
				Lister:        kubeInformersForNamespaces.InformersFor(rc.Signer.Namespace).Core().V1().Secrets().Lister(),
				Client:        kubeClient.CoreV1(),
				EventRecorder: eventRecorder,
			},
			certrotation.CABundleRotation{
				Namespace:     rc.CABundle.Namespace,
				Name:          rc.CABundle.Name,
				Informer:      kubeInformersForNamespaces.InformersFor(rc.CABundle.Namespace).Core().V1().ConfigMaps(),
				Lister:        kubeInformersForNamespaces.InformersFor(rc.CABundle.Namespace).Core().V1().ConfigMaps().Lister(),
				Client:        kubeClient.CoreV1(),
				EventRecorder: eventRecorder,
			},
			certrotation.TargetRotation{
				Namespace: rc.Namespace,
				Name:      rc.Name,
				Validity:  scaleToFakeDayDuration(rc.Validity, rotationDay),
				Refresh:   scaleToFakeDayDuration(rc.Refresh, rotationDay),
				CertCreator: &certrotation.ClientRotation{
					UserInfo: &user.DefaultInfo{Name: "system:openshift-aggregator"},
				},
				Informer:      kubeInformersForNamespaces.InformersFor(rc.Namespace).Core().V1().Secrets(),
				Lister:        kubeInformersForNamespaces.InformersFor(rc.Namespace).Core().V1().Secrets().Lister(),
				Client:        kubeClient.CoreV1(),
				EventRecorder: eventRecorder,
			},
			operatorClient,
		)
		if err != nil {
			return nil, fmt.Errorf("failed setting up the %q cert rotation controller: %v", rotator, err)
		}
		ret.certRotators[name] = rotator
	}

	return ret, nil
}

func scaleToFakeDayDuration(d time.Duration, day time.Duration) time.Duration {
	if day == time.Hour*24 {
		return d
	}
	return time.Duration(float64(d) / float64(time.Hour*24) * float64(day))
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

	for _, certRotator := range c.certRotators {
		certRotator.WaitForReady(stopCh)
	}
}

// RunOnce will run the cert rotation logic, but will not try to update the static pod status.
// This eliminates the need to pass an OperatorClient and avoids dubious writes and status.
func (c *CertRotationController) RunOnce() error {
	errlist := []error{}
	for _, certRotator := range c.certRotators {
		if err := certRotator.RunOnce(); err != nil {
			errlist = append(errlist, err)
		}
	}

	return utilerrors.NewAggregate(errlist)
}

func (c *CertRotationController) Run(workers int, stopCh <-chan struct{}) {
	klog.Infof("Starting CertRotation")
	defer klog.Infof("Shutting down CertRotation")
	c.WaitForReady(stopCh)

	go wait.Until(c.runServiceHostnames, time.Second, stopCh)
	go wait.Until(c.runExternalLoadBalancerHostnames, time.Second, stopCh)
	go wait.Until(c.runInternalLoadBalancerHostnames, time.Second, stopCh)

	for _, certRotator := range c.certRotators {
		go certRotator.Run(workers, stopCh)
	}
}
