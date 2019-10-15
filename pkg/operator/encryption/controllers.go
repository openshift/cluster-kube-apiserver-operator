package encryption

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/encryption/controllers"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/encryption/encryptionconfig"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/encryption/secrets"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

type runner interface {
	Run(stopCh <-chan struct{})
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
	encryptionSecretSelector := metav1.ListOptions{LabelSelector: secrets.EncryptionKeySecretsLabel + "=" + targetNamespace}

	if err := resourceSyncer.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespace, Name: encryptionconfig.EncryptionConfSecretName},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: destName},
	); err != nil {
		return nil, err
	}

	return &Controllers{
		controllers: []runner{
			controllers.NewKeyController(
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
			controllers.NewStateController(
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
			controllers.NewPruneController(
				targetNamespace,
				operatorClient,
				kubeInformersForNamespaces,
				kubeClient.CoreV1(),
				kubeClient.CoreV1(),
				encryptionSecretSelector,
				eventRecorder,
				encryptedGRs,
			),
			controllers.NewMigrationController(
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
		go con.Run(stopCh)
	}
	<-stopCh
}
