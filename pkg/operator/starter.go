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
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

func RunOperator(clientConfig *rest.Config, stopCh <-chan struct{}) error {
	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}
	dynamicClient, err := dynamic.NewForConfig(clientConfig)
	if err != nil {
		return err
	}

	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, targetNamespaceName, nil)

	operator := NewKubeApiserverOperator(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient.AppsV1(),
		kubeClient.CoreV1(),
		kubeClient.RbacV1(),
	)

	kubeInformersEtcdNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, etcdNamespaceName, nil)
	configObserver := NewConfigObserver(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersEtcdNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)
	v1alpha1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubeapiserveroperatorconfigs"},
		v1alpha1helpers.GetImageEnv,
	)

	operatorConfigInformers.Start(stopCh)
	kubeInformersNamespaced.Start(stopCh)
	kubeInformersEtcdNamespaced.Start(stopCh)

	go operator.Run(1, stopCh)
	go configObserver.Run(1, stopCh)

	<-stopCh
	return fmt.Errorf("stopped")
}
