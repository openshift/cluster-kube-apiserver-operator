package resourcesynccontroller

import (
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/tls"
)

func NewResourceSyncController(
	operatorConfigClient v1helpers.OperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder) (*resourcesynccontroller.ResourceSyncController, error) {

	resourceSyncController := resourcesynccontroller.NewResourceSyncController(
		operatorConfigClient,
		kubeInformersForNamespaces,
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		eventRecorder,
	)

	for _, cm := range []tls.SyncedConfigMap{
		tls.OpenShiftKubeAPIServer_EtcdServingCA,
		tls.OpenShiftKubeAPIServer_SATokenSigningCertsConfigMap,
		tls.OpenShiftKubeAPIServer_AggregatorClientCAConfigMap,
		tls.OpenShiftKubeAPIServer_KubeletServingCAConfigMap,
		tls.OpenShiftConfigManaged_KubeAPIServerClientCA,
		tls.OpenShiftConfigManaged_KubeletServingCA,
		tls.OpenShiftConfigManaged_KubeAPIServerServerCA,
	} {
		if err := resourceSyncController.SyncConfigMap(
			resourcesynccontroller.ResourceLocation{Namespace: cm.Namespace, Name: cm.Name},
			resourcesynccontroller.ResourceLocation{Namespace: cm.From.ToConfigMap().Namespace, Name: cm.From.ToConfigMap().Name},
		); err != nil {
			return nil, err
		}
	}

	for _, s := range []tls.SyncedSecret{
		tls.OpenShiftKubeAPIServer_EtcdClientSecret,
	} {
		if err := resourceSyncController.SyncSecret(
			resourcesynccontroller.ResourceLocation{Namespace: s.Namespace, Name: s.Name},
			resourcesynccontroller.ResourceLocation{Namespace: s.From.ToSecret().Namespace, Name: s.From.ToSecret().Name},
		); err != nil {
			return nil, err
		}
	}

	return resourceSyncController, nil
}
