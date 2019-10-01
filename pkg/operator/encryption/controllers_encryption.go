package encryption

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

type runner interface {
	run(stopCh <-chan struct{})
}

func NewEncryptionControllers(
	targetNamespace, destName string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	apiServerClient configv1client.APIServerInterface,
	apiServerInformer configv1informers.APIServerInformer,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	dynamicClient dynamic.Interface, // temporary hack for in-process storage migration
	groupResources ...schema.GroupResource,
) (*EncryptionControllers, error) {
	// avoid using the CachedSecretGetter as we need strong guarantees that our encryptionSecretSelector works
	// otherwise we could see secrets from a different component (which will break our keyID invariants)
	// this is fine in terms of performance since these controllers will be idle most of the time
	// TODO update the eventHandlers used by the controllers to ignore components that do not match their own
	secretClient := kubeClient.CoreV1()
	encryptionSecretSelector := metav1.ListOptions{LabelSelector: encryptionSecretComponent + "=" + targetNamespace}
	podClient := kubeClient.CoreV1().Pods(targetNamespace)

	if err := resourceSyncer.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespace, Name: encryptionConfSecret},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: destName},
	); err != nil {
		return nil, err
	}

	encryptedGRs := map[schema.GroupResource]bool{}
	for _, gr := range groupResources {
		encryptedGRs[gr] = true
	}

	return &EncryptionControllers{
		controllers: []runner{
			newEncryptionKeyController(
				targetNamespace,
				operatorClient,
				apiServerClient,
				apiServerInformer,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				secretClient,
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			newEncryptionStateController(
				targetNamespace,
				destName,
				operatorClient,
				kubeInformersForNamespaces,
				secretClient,
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
				kubeClient.CoreV1(),
			),
			newEncryptionPruneController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				secretClient,
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			newEncryptionMigrationController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				secretClient,
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
				podClient,
				dynamicClient,
				kubeClient.Discovery(),
			),
			newEncryptionPodStateController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				secretClient,
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
				podClient,
			),
		},
	}, nil
}

type EncryptionControllers struct {
	controllers []runner
}

func (c *EncryptionControllers) Run(stopCh <-chan struct{}) {
	for _, controller := range c.controllers {
		con := controller // capture range variable
		go con.run(stopCh)
	}
	<-stopCh
}
