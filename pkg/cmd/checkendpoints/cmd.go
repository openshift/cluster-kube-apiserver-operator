package checkendpoints

import (
	"context"
	"os"
	"time"

	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	operatorcontrolplaneinformers "github.com/openshift/client-go/operatorcontrolplane/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/controller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/spf13/cobra"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func NewCheckEndpointsCommand() *cobra.Command {
	config := controllercmd.NewControllerCommandConfig("check-endpoints", version.Get(), func(ctx context.Context, cctx *controllercmd.ControllerContext) error {
		namespace := os.Getenv("POD_NAMESPACE")
		kubeClient := kubernetes.NewForConfigOrDie(cctx.ProtoKubeConfig)
		operatorcontrolplaneClient := operatorcontrolplaneclient.NewForConfigOrDie(cctx.KubeConfig)
		kubeInformers := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(namespace))
		operatorcontrolplaneInformers := operatorcontrolplaneinformers.NewSharedInformerFactoryWithOptions(operatorcontrolplaneClient, 10*time.Minute, operatorcontrolplaneinformers.WithNamespace(namespace))
		check := controller.NewPodNetworkConnectivityCheckController(
			os.Getenv("POD_NAME"),
			namespace,
			operatorcontrolplaneClient.ControlplaneV1alpha1(),
			operatorcontrolplaneInformers.Controlplane().V1alpha1().PodNetworkConnectivityChecks(),
			kubeInformers.Core().V1().Secrets(),
			cctx.EventRecorder,
		)
		controller.RegisterMetrics()
		operatorcontrolplaneInformers.Start(ctx.Done())
		kubeInformers.Start(ctx.Done())
		go check.Run(ctx, 1)
		<-ctx.Done()
		return nil
	})
	config.DisableLeaderElection = true
	cmd := config.NewCommandWithContext(context.Background())
	cmd.Use = "check-endpoints"
	cmd.Short = "Checks that a tcp connection can be opened to one or more endpoints."
	return cmd
}
