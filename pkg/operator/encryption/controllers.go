package encryption

import (
	"k8s.io/client-go/tools/cache"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverconfig "k8s.io/apiserver/pkg/apis/config"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"

	"github.com/openshift/library-go/pkg/operator/management"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

var (
	apiserverScheme = runtime.NewScheme()
	apiserverCodecs = serializer.NewCodecFactory(apiserverScheme)
)

func init() {
	utilruntime.Must(apiserverconfigv1.AddToScheme(apiserverScheme))
	utilruntime.Must(apiserverconfig.AddToScheme(apiserverScheme))
}

func shouldRunEncryptionController(operatorClient operatorv1helpers.StaticPodOperatorClient) (bool, error) {
	operatorSpec, _, _, err := operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return false, err
	}

	return management.IsOperatorManaged(operatorSpec.ManagementState), nil
}

func setUpGlobalMachineConfigEncryptionInformers(
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	eventHandler cache.ResourceEventHandler,
) []cache.InformerSynced {
	operatorInformer := operatorClient.Informer()
	operatorInformer.AddEventHandler(eventHandler)

	secretsInformer := kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer()
	secretsInformer.AddEventHandler(eventHandler)

	return []cache.InformerSynced{
		operatorInformer.HasSynced,
		secretsInformer.HasSynced,
	}
}

func setUpAllEncryptionInformers(
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	targetNamespace string,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	eventHandler cache.ResourceEventHandler,
) []cache.InformerSynced {
	podInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Pods().Informer()
	podInformer.AddEventHandler(eventHandler)

	secretsInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer()
	secretsInformer.AddEventHandler(eventHandler)

	return append([]cache.InformerSynced{
		podInformer.HasSynced,
		secretsInformer.HasSynced,
	},
		setUpGlobalMachineConfigEncryptionInformers(operatorClient, kubeInformersForNamespaces, eventHandler)...)

}
