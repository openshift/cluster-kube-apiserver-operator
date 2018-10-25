package operator

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
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
	kubeInformersNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, targetNamespaceName, nil)
	kubeInformersEtcdNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, etcdNamespaceName, nil)

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

	prereqs := NewTargetConfigReconciler(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)
	deploymentController := NewDeploymentController(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)
	installerController := NewInstallerController(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)
	nodeController := NewNodeController(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersClusterScoped,
		operatorConfigClient.KubeapiserverV1alpha1(),
	)

	configObserver := NewConfigObserver(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersEtcdNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-apiserver",
		"openshift-kube-apiserver",
		dynamicClient,
		&operatorStatusProvider{informers: operatorConfigInformers},
	)

	operatorConfigInformers.Start(stopCh)
	kubeInformersClusterScoped.Start(stopCh)
	kubeInformersNamespaced.Start(stopCh)
	kubeInformersEtcdNamespaced.Start(stopCh)

	go prereqs.Run(1, stopCh)
	go deploymentController.Run(1, stopCh)
	go installerController.Run(1, stopCh)
	go nodeController.Run(1, stopCh)
	go configObserver.Run(1, stopCh)
	go clusterOperatorStatus.Run(1, stopCh)

	<-stopCh
	return fmt.Errorf("stopped")
}

type operatorStatusProvider struct {
	informers operatorclientinformers.SharedInformerFactory
}

func (p *operatorStatusProvider) Informer() cache.SharedIndexInformer {
	return p.informers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs().Informer()
}

func (p *operatorStatusProvider) CurrentStatus() (operatorv1alpha1.OperatorStatus, error) {
	instance, err := p.informers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return operatorv1alpha1.OperatorStatus{}, err
	}

	return instance.Status.OperatorStatus, nil
}
