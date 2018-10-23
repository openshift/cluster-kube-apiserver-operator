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
	kubeInformersForOpenshiftKubeAPIServerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(targetNamespaceName))
	kubeInformersForKubeSystemNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system"))
	kubeInformersForOpenshiftServiceCertSignerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(serviceCertSignerNamespaceName))

	v1alpha1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubeapiserveroperatorconfigs"},
		v1alpha1helpers.GetImageEnv,
	)

	operator := NewKubeApiserverOperator(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient.AppsV1(),
		kubeClient.CoreV1(),
		kubeClient.RbacV1(),
	)

	configObserver := NewConfigObserver(
		operatorConfigInformers,
		kubeInformersForKubeSystemNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeInformersForOpenshiftServiceCertSignerNamespace,
		kubeClient,
		clientConfig,
	)

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-apiserver",
		"openshift-kube-apiserver",
		dynamicClient,
		&operatorStatusProvider{informers: operatorConfigInformers},
	)

	operatorConfigInformers.Start(stopCh)
	kubeInformersForOpenshiftKubeAPIServerNamespace.Start(stopCh)
	kubeInformersForKubeSystemNamespace.Start(stopCh)
	kubeInformersForOpenshiftServiceCertSignerNamespace.Start(stopCh)

	go operator.Run(1, stopCh)
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
