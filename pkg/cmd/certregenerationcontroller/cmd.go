package certregenerationcontroller

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes"

	operatorv1 "github.com/openshift/api/operator/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configexternalinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
}

func NewCertRegenerationControllerCommand(ctx context.Context) *cobra.Command {
	o := &Options{}

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

		err = o.Run(ctx)
		if err != nil {
			return err
		}

		return nil
	})

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

func (o *Options) Run(ctx context.Context) error {
	kubeClient, err := kubernetes.NewForConfig(o.controllerContext.ProtoKubeConfig)
	if err != nil {
		return fmt.Errorf("can't build kubernetes client: %w", err)
	}

	configClient, err := configeversionedclient.NewForConfig(o.controllerContext.KubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)

	kubeAPIServerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	)

	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(o.controllerContext.KubeConfig, operatorv1.GroupVersion.WithResource("kubeapiservers"))
	if err != nil {
		return err
	}

	certRotationScale, err := certrotation.GetCertRotationScale(ctx, kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
	if err != nil {
		return err
	}

	kubeAPIServerCertRotationController, err := certrotationcontroller.NewCertRotationControllerOnlyWhenExpired(
		kubeClient,
		operatorClient,
		configInformers,
		kubeAPIServerInformersForNamespaces,
		o.controllerContext.EventRecorder,
		certRotationScale,
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
	configInformers.Start(ctx.Done())
	kubeAPIServerInformersForNamespaces.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())

	// FIXME: These are missing a wait group to track goroutines and handle graceful termination
	// (@deads2k wants time to think it through)

	go func() {
		kubeAPIServerCertRotationController.Run(ctx, 1)
	}()

	go func() {
		caBundleController.Run(ctx)
	}()

	<-ctx.Done()

	return nil
}
