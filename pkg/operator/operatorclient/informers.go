package operatorclient

import (
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

type KubeInformersForNamespaces map[string]informers.SharedInformerFactory

func NewKubeInformersForNamespaces(kubeClient kubernetes.Interface) KubeInformersForNamespaces {
	return map[string]informers.SharedInformerFactory{
		"": informers.NewSharedInformerFactory(kubeClient, 10*time.Minute),
		UserSpecifiedGlobalConfigNamespace:    informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(UserSpecifiedGlobalConfigNamespace)),
		MachineSpecifiedGlobalConfigNamespace: informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(MachineSpecifiedGlobalConfigNamespace)),
		OperatorNamespace:                     informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(OperatorNamespace)),
		TargetNamespaceName:                   informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(TargetNamespaceName)),
		"kube-system":                         informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system")),
	}
}

func (i KubeInformersForNamespaces) Start(stopCh <-chan struct{}) {
	for _, informer := range i {
		informer.Start(stopCh)
	}
}
