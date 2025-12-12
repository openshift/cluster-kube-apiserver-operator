package certregenerationcontroller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"

	operatorv1 "github.com/openshift/api/operator/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configexternalinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
}

func NewCertRegenerationControllerCommand(ctx context.Context) *cobra.Command {
	o := &Options{}
	c := clock.RealClock{}

	ccc := controllercmd.NewControllerCommandConfig("cert-regeneration-controller", version.Get(), func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
		o.controllerContext = controllerContext

		err := o.Validate(ctx)
		if err != nil {
			return err
		}

		err = o.Complete(ctx)
		if err != nil {
			return err
		}

		err = o.Run(ctx, c)
		if err != nil {
			return err
		}

		return nil
	}, c)

	// Disable serving for recovery as it introduces a dependency on kube-system::extension-apiserver-authentication
	// configmap which prevents it to start as the CA bundle is expired.
	// TODO: Remove when the internal logic can start serving without extension-apiserver-authentication
	//  	 and live reload extension-apiserver-authentication after it is available
	ccc.DisableServing = true

	cmd := ccc.NewCommandWithContext(ctx)
	cmd.Use = "cert-regeneration-controller"
	cmd.Short = "Start the Cluster Certificate Regeneration Controller"

	return cmd
}

func (o *Options) Validate(ctx context.Context) error {
	return nil
}

func (o *Options) Complete(ctx context.Context) error {
	return nil
}

func (o *Options) Run(ctx context.Context, clock clock.Clock) error {
	kubeClient, err := kubernetes.NewForConfig(o.controllerContext.ProtoKubeConfig)
	if err != nil {
		return fmt.Errorf("can't build kubernetes client: %w", err)
	}

	configClient, err := configeversionedclient.NewForConfig(o.controllerContext.KubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	kubeAPIServerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	)

	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)

	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(
		clock,
		o.controllerContext.KubeConfig,
		operatorv1.GroupVersion.WithResource("kubeapiservers"),
		operatorv1.GroupVersion.WithKind("KubeAPIServer"),
		operator.ExtractStaticPodOperatorSpec,
		operator.ExtractStaticPodOperatorStatus,
	)
	if err != nil {
		return err
	}

	desiredVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(), configInformers.Config().V1().FeatureGates(),
		o.controllerContext.EventRecorder,
	)

	var wg sync.WaitGroup
	defer wg.Wait()
	// cancel must happen before wg.Wait (so in a later defer), otherwise we can get stuck on early return.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	configInformers.Start(ctx.Done())

	wg.Add(1)
	go func() {
		defer wg.Done()
		featureGateAccessor.Run(ctx)
	}()

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: knownFeatureGates=%v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		klog.Errorf("timed out waiting for FeatureGate detection")
		return fmt.Errorf("timed out waiting for FeatureGate detection")
	}

	kubeAPIServerCertRotationController, err := certrotationcontroller.NewCertRotationControllerOnlyWhenExpired(
		kubeClient,
		operatorClient,
		configInformers,
		kubeAPIServerInformersForNamespaces,
		o.controllerContext.EventRecorder,
		featureGateAccessor,
	)
	if err != nil {
		return err
	}

	caBundleController, err := NewCABundleController(
		kubeClient.CoreV1(),
		kubeAPIServerInformersForNamespaces,
		o.controllerContext.EventRecorder,
	)
	if err != nil {
		return err
	}

	// We can't start informers until after the resources have been requested. Now is the time.
	kubeAPIServerInformersForNamespaces.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())
	configInformers.Start(ctx.Done())

	wg.Add(1)
	go func() {
		defer wg.Done()
		kubeAPIServerCertRotationController.Run(ctx, 1)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		caBundleController.Run(ctx)
	}()

	<-ctx.Done()

	return nil
}
