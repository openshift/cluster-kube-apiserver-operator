package operator

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

func RunOperator(clientConfig *rest.Config, stopCh <-chan struct{}) error {
	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersClusterScoped := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	kubeInformersForOpenshiftKubeAPIServerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(targetNamespaceName))
	kubeInformersForKubeSystemNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system"))
	staticPodOperatorClient := &staticPodOperatorClient{
		informers: operatorConfigInformers,
		client:    operatorConfigClient.KubeapiserverV1alpha1(),
	}

	v1alpha1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubeapiserveroperatorconfigs"},
		v1alpha1helpers.GetImageEnv,
	)

	// meet our control loops.  Each has a specific job and they operator independently so that each is very simply to write and test
	// 1. targetConfigReconciler - this creates configmaps and secrets to be copied for static pods.  It writes to a single target for each resource
	//    (no spin numbers on the individual secrets).  This (and other content) are targets for the deploymentContent loop.
	// 2. deploymentController - this watches multiple resources for "latest" input that has changed from the most current deploymentID.
	//    When a change is found, it creates a new deployment by copying resources and adding the deploymentID suffix to the names
	//    to make a theoretically immutable set of deployment data.  It then bumps the latestDeploymentID and starts watching again.
	// 3. installerController - this watches the latestDeploymentID and the list of kubeletStatus (alpha-sorted list).  When a latestDeploymentID
	//    appears that doesn't match the current latest for first kubeletStatus and the first kubeletStatus isn't already transitioning,
	//    it kicks off an installer pod.  If the next kubeletStatus doesn't match the immediate prior one, it kicks off that transition.
	// 4. nodeController - watches nodes for master nodes and keeps the operator status up to date

	configObserver := NewConfigObserver(
		operatorConfigInformers,
		kubeInformersForKubeSystemNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)
	targetConfigReconciler := NewTargetConfigReconciler(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs(),
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)

	staticPodControllers := staticpod.NewControllers(
		targetNamespaceName,
		[]string{"cluster-kube-apiserver-operator", "installer"},
		deploymentConfigMaps,
		deploymentSecrets,
		staticPodOperatorClient,
		kubeClient,
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		kubeInformersClusterScoped,
	)

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-apiserver",
		"openshift-kube-apiserver",
		dynamicClient,
		staticPodOperatorClient,
	)

	operatorConfigInformers.Start(stopCh)
	kubeInformersClusterScoped.Start(stopCh)
	kubeInformersForOpenshiftKubeAPIServerNamespace.Start(stopCh)
	kubeInformersForKubeSystemNamespace.Start(stopCh)

	go staticPodControllers.Run(stopCh)
	go targetConfigReconciler.Run(1, stopCh)
	go configObserver.Run(1, stopCh)
	go clusterOperatorStatus.Run(1, stopCh)

	<-stopCh
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []string{
	"kube-apiserver-pod",
	"deployment-kube-apiserver-config",
	"aggregator-client-ca",
	"client-ca",
	"etcd-serving-ca",
	"kubelet-serving-ca",
	"sa-token-signing-certs",
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []string{
	"aggregator-client",
	"etcd-client",
	"kubelet-client",
	"serving-cert",
}
