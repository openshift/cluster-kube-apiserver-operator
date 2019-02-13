package configobservercontroller

import (
	"k8s.io/client-go/tools/cache"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	operatorv1informers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/apiserver"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/auth"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/etcd"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/images"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/network"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/satokencerts"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient v1helpers.OperatorClient,
	operatorConfigInformers operatorv1informers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	configInformer configinformers.SharedInformerFactory,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	eventRecorder events.Recorder,
) *ConfigObserver {
	interestingNamespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
		"kube-system",
	}

	preRunCaches := []cache.InformerSynced{
		operatorConfigInformers.Operator().V1().KubeAPIServers().Informer().HasSynced,
		kubeInformersForNamespaces.InformersFor("kube-system").Core().V1().Endpoints().Informer().HasSynced,
		configInformer.Config().V1().Authentications().Informer().HasSynced,
		configInformer.Config().V1().Images().Informer().HasSynced,
	}
	for _, ns := range interestingNamespaces {
		preRunCaches = append(preRunCaches, kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().HasSynced)
	}

	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				AuthConfigLister:  configInformer.Config().V1().Authentications().Lister(),
				ImageConfigLister: configInformer.Config().V1().Images().Lister(),
				ConfigmapLister:   kubeInformersForNamespaces.ConfigMapLister(),
				EndpointsLister:   kubeInformersForNamespaces.InformersFor("kube-system").Core().V1().Endpoints().Lister(),
				APIServerLister:   configInformer.Config().V1().APIServers().Lister(),
				ResourceSync:      resourceSyncer,
				PreRunCachesSynced: []cache.InformerSynced{
					operatorConfigInformers.Operator().V1().KubeAPIServers().Informer().HasSynced,
					kubeInformersForNamespaces.InformersFor("kube-system").Core().V1().Endpoints().Informer().HasSynced,
					configInformer.Config().V1().Authentications().Informer().HasSynced,
					configInformer.Config().V1().Images().Informer().HasSynced,
					configInformer.Config().V1().APIServers().Informer().HasSynced,
				},
			},
			apiserver.ObserveDefaultUserServingCertificate,
			apiserver.ObserveNamedCertificates,
			apiserver.ObserveUserClientCABundle,
			auth.ObserveAuthMetadata,
			etcd.ObserveStorageURLs,
			network.ObserveRestrictedCIDRs,
			images.ObserveInternalRegistryHostname,
			images.ObserveExternalRegistryHostnames,
			images.ObserveAllowedRegistriesForImport,
			satokencerts.ObserveSATokenCerts,
		),
	}

	for _, ns := range interestingNamespaces {
		kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())
	}
	operatorConfigInformers.Operator().V1().KubeAPIServers().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForNamespaces.InformersFor("kube-system").Core().V1().Endpoints().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Images().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Authentications().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().APIServers().Informer().AddEventHandler(c.EventHandler())

	return c
}
