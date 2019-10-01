package configobservercontroller

import (
	"k8s.io/client-go/tools/cache"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/configobserver/proxy"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/apiserver"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/auth"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/etcd"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/images"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/network"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/scheduler"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/encryption"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient v1helpers.OperatorClient,
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
	}

	corePreRunCacheSynced := []cache.InformerSynced{}
	for _, ns := range interestingNamespaces {
		corePreRunCacheSynced = append(corePreRunCacheSynced,
			kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(ns).Core().V1().Secrets().Informer().HasSynced,
		)
	}

	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				APIServerLister:       configInformer.Config().V1().APIServers().Lister(),
				AuthConfigLister:      configInformer.Config().V1().Authentications().Lister(),
				FeatureGateLister_:    configInformer.Config().V1().FeatureGates().Lister(),
				ImageConfigLister:     configInformer.Config().V1().Images().Lister(),
				InfrastructureLister_: configInformer.Config().V1().Infrastructures().Lister(),
				NetworkLister:         configInformer.Config().V1().Networks().Lister(),
				ProxyLister_:          configInformer.Config().V1().Proxies().Lister(),
				SchedulerLister:       configInformer.Config().V1().Schedulers().Lister(),

				ConfigmapLister:              kubeInformersForNamespaces.ConfigMapLister(),
				SecretLister_:                kubeInformersForNamespaces.SecretLister(),
				OpenshiftEtcdEndpointsLister: kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().Endpoints().Lister(),

				ResourceSync: resourceSyncer,
				PreRunCachesSynced: append(corePreRunCacheSynced,
					operatorClient.Informer().HasSynced,

					kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().Endpoints().Informer().HasSynced,

					configInformer.Config().V1().APIServers().Informer().HasSynced,
					configInformer.Config().V1().Authentications().Informer().HasSynced,
					configInformer.Config().V1().FeatureGates().Informer().HasSynced,
					configInformer.Config().V1().Images().Informer().HasSynced,
					configInformer.Config().V1().Infrastructures().Informer().HasSynced,
					configInformer.Config().V1().Networks().Informer().HasSynced,
					configInformer.Config().V1().Proxies().Informer().HasSynced,
					configInformer.Config().V1().Schedulers().Informer().HasSynced,
				),
			},
			// We are disabling this because it doesn't work today and customers aren't going to be able to get the kube service network options right.
			// Customers may only use SNI.  I'm leaving this code in case we ever come up with a way to make an SNI-like thing based on IPs.
			//apiserver.ObserveDefaultUserServingCertificate,
			apiserver.ObserveNamedCertificates,
			apiserver.ObserveUserClientCABundle,
			apiserver.ObserveAdditionalCORSAllowedOrigins,
			auth.ObserveAuthMetadata,
			encryption.NewEncryptionObserver(
				operatorclient.TargetNamespace,
				[]string{"apiServerArguments", "encryption-provider-config"},
			),
			etcd.ObserveStorageURLs,
			cloudprovider.NewCloudProviderObserver(
				"openshift-kube-apiserver",
				[]string{"apiServerArguments", "cloud-provider"},
				[]string{"apiServerArguments", "cloud-config"}),
			featuregates.NewObserveFeatureFlagsFunc(nil, []string{"apiServerArguments", "feature-gates"}),
			network.ObserveRestrictedCIDRs,
			network.ObserveServicesSubnet,
			network.ObserveExternalIPPolicy,
			proxy.NewProxyObserveFunc([]string{"targetconfigcontroller", "proxy"}),
			images.ObserveInternalRegistryHostname,
			images.ObserveExternalRegistryHostnames,
			images.ObserveAllowedRegistriesForImport,
			scheduler.ObserveDefaultNodeSelector,
		),
	}

	operatorClient.Informer().AddEventHandler(c.EventHandler())

	for _, ns := range interestingNamespaces {
		kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())
		kubeInformersForNamespaces.InformersFor(ns).Core().V1().Secrets().Informer().AddEventHandler(c.EventHandler())
	}
	kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().Endpoints().Informer().AddEventHandler(c.EventHandler())

	configInformer.Config().V1().Images().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Infrastructures().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Authentications().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().APIServers().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Networks().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Proxies().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Schedulers().Informer().AddEventHandler(c.EventHandler())

	return c
}
