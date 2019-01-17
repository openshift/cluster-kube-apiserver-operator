package operatorclient

import (
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/client-go/kubernetes"
)

func NewKubeInformersForNamespaces(kubeClient kubernetes.Interface) v1helpers.KubeInformersForNamespaces {
	return v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		UserSpecifiedGlobalConfigNamespace,
		MachineSpecifiedGlobalConfigNamespace,
		OperatorNamespace,
		TargetNamespaceName,
		"kube-system",
	)
}
