package encryption

import (
	"k8s.io/client-go/tools/cache"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverconfig "k8s.io/apiserver/pkg/apis/config"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
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

type runner interface {
	run(stopCh <-chan struct{})
}

func NewControllers(
	targetNamespace, destName string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	apiServerClient configv1client.APIServerInterface,
	apiServerInformer configv1informers.APIServerInformer,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	dynamicClient dynamic.Interface, // temporary hack for in-process storage migration
	encryptedGRs ...schema.GroupResource,
) (*Controllers, error) {
	// avoid using the CachedSecretGetter as we need strong guarantees that our encryptionSecretSelector works
	// otherwise we could see secrets from a different component (which will break our keyID invariants)
	// this is fine in terms of performance since these controllers will be idle most of the time
	// TODO: update the eventHandlers used by the controllers to ignore components that do not match their own
	encryptionSecretSelector := metav1.ListOptions{LabelSelector: encryptionSecretComponent + "=" + targetNamespace}

	if err := resourceSyncer.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespace, Name: encryptionConfSecret},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: destName},
	); err != nil {
		return nil, err
	}

	return &Controllers{
		controllers: []runner{
			newKeyController(
				targetNamespace,
				operatorClient,
				apiServerClient,
				apiServerInformer,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				kubeClient.CoreV1(),
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			newStateController(
				targetNamespace,
				destName,
				operatorClient,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				kubeClient.CoreV1(),
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			newPruneController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				kubeClient.CoreV1(),
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			newMigrationController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
				kubeClient.CoreV1(),
				dynamicClient,
				kubeClient.Discovery(),
			),
		},
	}, nil
}

type Controllers struct {
	controllers []runner
}

func (c *Controllers) Run(stopCh <-chan struct{}) {
	for _, controller := range c.controllers {
		con := controller // capture range variable
		go con.run(stopCh)
	}
	<-stopCh
}

func shouldRunEncryptionController(operatorClient operatorv1helpers.StaticPodOperatorClient) (bool, error) {
	operatorSpec, _, _, err := operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return false, err
	}

	return management.IsOperatorManaged(operatorSpec.ManagementState), nil
}

func setUpInformers(
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	targetNamespace string,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	eventHandler cache.ResourceEventHandler,
) []cache.InformerSynced {
	targetPodInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Pods().Informer()
	targetPodInformer.AddEventHandler(eventHandler)

	targetSecretsInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer()
	targetSecretsInformer.AddEventHandler(eventHandler)

	operatorInformer := operatorClient.Informer()
	operatorInformer.AddEventHandler(eventHandler)

	managedSecretsInformer := kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer()
	managedSecretsInformer.AddEventHandler(eventHandler)

	return []cache.InformerSynced{
		targetPodInformer.HasSynced,
		targetSecretsInformer.HasSynced,
		operatorInformer.HasSynced,
		managedSecretsInformer.HasSynced,
	}
}
