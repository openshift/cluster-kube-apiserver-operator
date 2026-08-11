package configobservercontroller

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	operatorv1informers "github.com/openshift/client-go/operator/informers/externalversions/operator/v1"
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

func NewConfigObserver(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	apiServerInformer configv1informers.APIServerInformer,
	authInformer configv1informers.AuthenticationInformer,
	featureGateInformer configv1informers.FeatureGateInformer,
	imageInformer configv1informers.ImageInformer,
	infraInformer configv1informers.InfrastructureInformer,
	networkInformer configv1informers.NetworkInformer,
	nodeInformer configv1informers.NodeInformer,
	oauthInformer configv1informers.OAuthInformer,
	proxyInformer configv1informers.ProxyInformer,
	schedulerInformer configv1informers.SchedulerInformer,
	kubeAPIServerInformer operatorv1informers.KubeAPIServerInformer,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	featureGateAccessor featuregates.FeatureGateAccess,
	eventRecorder events.Recorder,
	groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion,
) *ConfigObserver {
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
		imageInformer.Informer(),
		infraInformer.Informer(),
		authInformer.Informer(),
		apiServerInformer.Informer(),
		networkInformer.Informer(),
		nodeInformer.Informer(),
		proxyInformer.Informer(),
		schedulerInformer.Informer(),
		kubeAPIServerInformer.Informer(),
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
				APIServerLister_:      apiServerInformer.Lister(),
				AuthConfigLister:      authInformer.Lister(),
				FeatureGateLister_:    featureGateInformer.Lister(),
				ImageConfigLister:     imageInformer.Lister(),
				InfrastructureLister_: infraInformer.Lister(),
				NetworkLister:         networkInformer.Lister(),
				NodeLister_:           nodeInformer.Lister(),
				ProxyLister_:          proxyInformer.Lister(),
				SchedulerLister:       schedulerInformer.Lister(),

				SecretLister_:       kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
				ConfigSecretLister_: kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
				ConfigmapLister_:    kubeInformersForNamespaces.ConfigMapLister(),

				KubeAPIServerOperatorLister_: kubeAPIServerInformer.Lister(),

				ResourceSync: resourceSyncer,
				PreRunCachesSynced: append(preRunCacheSynced,
					operatorClient.Informer().HasSynced,
					kubeAPIServerInformer.Informer().HasSynced,

					kubeInformersForNamespaces.InformersFor("openshift-etcd").Core().V1().ConfigMaps().Informer().HasSynced,
					kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().HasSynced,

					apiServerInformer.Informer().HasSynced,
					authInformer.Informer().HasSynced,
					featureGateInformer.Informer().HasSynced,
					imageInformer.Informer().HasSynced,
					infraInformer.Informer().HasSynced,
					networkInformer.Informer().HasSynced,
					nodeInformer.Informer().HasSynced,
					oauthInformer.Informer().HasSynced,
					proxyInformer.Informer().HasSynced,
					schedulerInformer.Informer().HasSynced,
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
			apiserver.ObserveGoawayChance,
			apiserver.ObserveAdmissionPlugins,
			apiserver.NewObserveEventTTL(featureGateAccessor),
			libgoapiserver.ObserveTLSSecurityProfile,
			auth.NewObserveAuthMetadata(featureGateAccessor),
			auth.ObserveServiceAccountIssuer,
			auth.NewObserveWebhookTokenAuthenticator(featureGateAccessor),
			auth.NewObserveExternalOIDC(featureGateAccessor),
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
			node.NewMinimumKubeletVersionObserver(featureGateAccessor),
			node.NewAuthorizationModeObserver(featureGateAccessor),
			proxy.NewProxyObserveFunc([]string{"targetconfigcontroller", "proxy"}),
			images.ObserveInternalRegistryHostname,
			images.ObserveExternalRegistryHostnames,
			images.ObserveAllowedRegistriesForImport,
			scheduler.ObserveDefaultNodeSelector,
		),
	}

	return c
}
