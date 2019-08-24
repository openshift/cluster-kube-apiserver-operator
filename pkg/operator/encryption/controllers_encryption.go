package encryption

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

type runner interface {
	run(stopCh <-chan struct{})
}

func NewEncryptionControllers(
	targetNamespace, destName string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	dynamicClient dynamic.Interface, // temporary hack for in-process storage migration
	groupResources ...schema.GroupResource,
) (*EncryptionControllers, error) {
	secretClient := v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces)
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
				kubeInformersForNamespaces,
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
				podClient,
			),
			newEncryptionPruneController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
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
