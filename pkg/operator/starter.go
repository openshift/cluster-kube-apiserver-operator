package operator

import (
	"fmt"
	"os"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/aggregatorproxyrotation"
	"k8s.io/apiserver/pkg/authentication/user"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func RunOperator(ctx *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersClusterScoped := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	kubeInformersForOpenshiftKubeAPIServerOperatorNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("openshift-cluster-kube-apiserver-operator"))
	kubeInformersForOpenshiftKubeAPIServerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(targetNamespaceName))
	kubeInformersForKubeSystemNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system"))
	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	staticPodOperatorClient := &staticPodOperatorClient{
		informers: operatorConfigInformers,
		client:    operatorConfigClient.KubeapiserverV1alpha1(),
	}

	v1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubeapiserveroperatorconfigs"},
	)

	configObserver := configobservercontroller.NewConfigObserver(
		staticPodOperatorClient,
		operatorConfigInformers,
		kubeInformersForKubeSystemNamespace,
		configInformers,
		ctx.EventRecorder,
	)
	targetConfigReconciler := NewTargetConfigReconciler(
		os.Getenv("IMAGE"),
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs(),
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
		ctx.EventRecorder,
	)

	staticPodControllers := staticpod.NewControllers(
		targetNamespaceName,
		"openshift-kube-apiserver",
		[]string{"cluster-kube-apiserver-operator", "installer"},
		[]string{"cluster-kube-apiserver-operator", "prune"},
		deploymentConfigMaps,
		deploymentSecrets,
		staticPodOperatorClient,
		kubeClient,
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		kubeInformersClusterScoped,
		ctx.EventRecorder,
		10,
		10,
	)
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-cluster-kube-apiserver-operator",
		configClient.ConfigV1(),
		staticPodOperatorClient,
		ctx.EventRecorder,
	)
	aggregatorProxyClientCertController := aggregatorproxyrotation.NewClientCertRotationController(
		"AggregatorProxyClientCerts",
		"openshift-cluster-kube-apiserver-operator",
		1*24*time.Hour,
		0.5,
		"aggregator-proxy-client-signer",
		"openshift-cluster-kube-apiserver-operator",
		"aggregator-proxy-client-ca-bundle",
		"openshift-cluster-kube-apiserver-operator",
		1*time.Hour,
		0.75,
		"aggregator-proxy-client-cert-key",
		&user.DefaultInfo{},
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Core().V1().ConfigMaps(),
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Core().V1().Secrets(),
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		ctx.EventRecorder,
	)

	operatorConfigInformers.Start(ctx.StopCh)
	kubeInformersClusterScoped.Start(ctx.StopCh)
	kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Start(ctx.StopCh)
	kubeInformersForOpenshiftKubeAPIServerNamespace.Start(ctx.StopCh)
	kubeInformersForKubeSystemNamespace.Start(ctx.StopCh)
	configInformers.Start(ctx.StopCh)

	go staticPodControllers.Run(ctx.StopCh)
	go targetConfigReconciler.Run(1, ctx.StopCh)
	go configObserver.Run(1, ctx.StopCh)
	go clusterOperatorStatus.Run(1, ctx.StopCh)
	go aggregatorProxyClientCertController.Run(1, ctx.StopCh)

	<-ctx.StopCh
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []string{
	"kube-apiserver-pod",
	"config",
	"aggregator-client-ca",
	"client-ca",
	"etcd-serving-ca",
	"kubelet-serving-ca",
	"sa-token-signing-certs",
	"revision-status",
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []string{
	"aggregator-client",
	"etcd-client",
	"kubelet-client",
	"serving-cert",
}
