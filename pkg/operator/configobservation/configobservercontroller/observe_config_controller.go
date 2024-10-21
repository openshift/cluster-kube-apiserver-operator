package configobservercontroller

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	operatorv1informers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	libgoapiserver "github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	nodeobserver "github.com/openshift/library-go/pkg/operator/configobserver/node"
	"github.com/openshift/library-go/pkg/operator/configobserver/proxy"
	encryption "github.com/openshift/library-go/pkg/operator/encryption/observer"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/apienablement"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/apiserver"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/auth"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/etcdendpoints"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/images"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/network"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/node"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/scheduler"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

var FeatureBlacklist sets.Set[configv1.FeatureGateName]

type ConfigObserver struct {
	factory.Controller
}

func NewConfigObserver(operatorClient v1helpers.StaticPodOperatorClient, kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces, configInformer configinformers.SharedInformerFactory, operatorInformer operatorv1informers.SharedInformerFactory, resourceSyncer resourcesynccontroller.ResourceSyncer, featureGateAccessor featuregates.FeatureGateAccess, eventRecorder events.Recorder, groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion) *ConfigObserver {
	interestingNamespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
	}

	preRunCacheSynced := []cache.InformerSynced{}
	for _, ns := range interestingNamespaces {
		preRunCacheSynced = append(preRunCacheSynced,
			kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(ns).Core().V1().Secrets().Informer().HasSynced,
		)
	}

	infomers := []factory.Informer{
		operatorClient.Informer(),
		kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().Endpoints().Informer(),
		kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().ConfigMaps().Informer(),
		configInformer.Config().V1().Images().Informer(),
		configInformer.Config().V1().Infrastructures().Informer(),
		configInformer.Config().V1().Authentications().Informer(),
		configInformer.Config().V1().APIServers().Informer(),
		configInformer.Config().V1().Networks().Informer(),
		configInformer.Config().V1().Nodes().Informer(),
		configInformer.Config().V1().Proxies().Informer(),
		configInformer.Config().V1().Schedulers().Informer(),
		operatorInformer.Operator().V1().KubeAPIServers().Informer(),
	}
	for _, ns := range interestingNamespaces {
		infomers = append(infomers, kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer())
	}

	c := &ConfigObserver{
		Controller: configobserver.NewConfigObserver(
			"kube-apiserver",
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				APIServerLister_:      configInformer.Config().V1().APIServers().Lister(),
				AuthConfigLister:      configInformer.Config().V1().Authentications().Lister(),
				FeatureGateLister_:    configInformer.Config().V1().FeatureGates().Lister(),
				ImageConfigLister:     configInformer.Config().V1().Images().Lister(),
				InfrastructureLister_: configInformer.Config().V1().Infrastructures().Lister(),
				NetworkLister:         configInformer.Config().V1().Networks().Lister(),
				NodeLister_:           configInformer.Config().V1().Nodes().Lister(),
				ProxyLister_:          configInformer.Config().V1().Proxies().Lister(),
				SchedulerLister:       configInformer.Config().V1().Schedulers().Lister(),

				SecretLister_:       kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
				ConfigSecretLister_: kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
				ConfigmapLister_:    kubeInformersForNamespaces.ConfigMapLister(),

				KubeAPIServerOperatorLister_: operatorInformer.Operator().V1().KubeAPIServers().Lister(),

				ResourceSync: resourceSyncer,
				PreRunCachesSynced: append(preRunCacheSynced,
					operatorClient.Informer().HasSynced,

					kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().ConfigMaps().Informer().HasSynced,
					kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().HasSynced,

					configInformer.Config().V1().APIServers().Informer().HasSynced,
					configInformer.Config().V1().Authentications().Informer().HasSynced,
					configInformer.Config().V1().FeatureGates().Informer().HasSynced,
					configInformer.Config().V1().Images().Informer().HasSynced,
					configInformer.Config().V1().Infrastructures().Informer().HasSynced,
					configInformer.Config().V1().Networks().Informer().HasSynced,
					configInformer.Config().V1().Nodes().Informer().HasSynced,
					configInformer.Config().V1().OAuths().Informer().HasSynced,
					configInformer.Config().V1().Proxies().Informer().HasSynced,
					configInformer.Config().V1().Schedulers().Informer().HasSynced,
				),
			},
			infomers,
			// We are disabling this because it doesn't work today and customers aren't going to be able to get the kube service network options right.
			// Customers may only use SNI.  I'm leaving this code in case we ever come up with a way to make an SNI-like thing based on IPs.
			//apiserver.ObserveDefaultUserServingCertificate,
			apiserver.ObserveNamedCertificates,
			apiserver.ObserveUserClientCABundle,
			apiserver.ObserveAdditionalCORSAllowedOrigins,
			apiserver.ObserveShutdownDelayDuration,
			apiserver.ObserveGracefulTerminationDuration,
			apiserver.ObserveSendRetryAfterWhileNotReadyOnce,
			libgoapiserver.ObserveTLSSecurityProfile,
			auth.ObserveAuthMetadata,
			auth.ObserveServiceAccountIssuer,
			auth.ObserveWebhookTokenAuthenticator,
			auth.NewObservePodSecurityAdmissionEnforcementFunc(featureGateAccessor),
			encryption.NewEncryptionConfigObserver(
				operatorclient.TargetNamespace,
				// static path at which we expect to find the encryption config secret
				"/etc/kubernetes/static-pod-resources/secrets/encryption-config/encryption-config",
			),
			etcdendpoints.ObserveStorageURLs,
			cloudprovider.NewCloudProviderObserver(
				"openshift-kube-apiserver", true,
			),
			apienablement.NewFeatureGateObserverWithRuntimeConfig(
				nil,
				FeatureBlacklist,
				featureGateAccessor,
				groupVersionsByFeatureGate,
			),
			network.ObserveRestrictedCIDRs,
			network.ObserveServicesSubnet,
			network.ObserveExternalIPPolicy,
			network.ObserveServicesNodePortRange,
			nodeobserver.NewLatencyProfileObserver(
				node.LatencyConfigs,
				[]nodeobserver.ShouldSuppressConfigUpdatesFunc{
					nodeobserver.NewSuppressConfigUpdateUntilSameProfileFunc(
						operatorClient,
						kubeInformersForNamespaces.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace),
						node.LatencyConfigs,
					),
				},
			),
			nodeobserver.ObserveMinimumKubeletVersion,
			proxy.NewProxyObserveFunc([]string{"targetconfigcontroller", "proxy"}),
			images.ObserveInternalRegistryHostname,
			images.ObserveExternalRegistryHostnames,
			images.ObserveAllowedRegistriesForImport,
			scheduler.ObserveDefaultNodeSelector,
		),
	}

	return c
}
