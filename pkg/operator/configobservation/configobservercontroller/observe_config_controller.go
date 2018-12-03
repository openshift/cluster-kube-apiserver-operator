package configobservercontroller

import (
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	kubeapiserveroperatorinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/authconfig"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/etcd"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/images"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/network"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient configobserver.OperatorClient,
	operatorConfigInformers kubeapiserveroperatorinformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
	kubeInformersForOpenshiftKubeAPIServerNamespace kubeinformers.SharedInformerFactory,
	configInformer configinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
) *ConfigObserver {
	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				ImageConfigLister: configInformer.Config().V1().Images().Lister(),
				EndpointsLister:   kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Lister(),
				ConfigmapLister:   kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
				SecretLister:      kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets().Lister(),
				SecretHasSynced:   kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets().Informer().HasSynced,
				ImageConfigSynced: configInformer.Config().V1().Images().Informer().HasSynced,
				PreRunCachesSynced: []cache.InformerSynced{
					operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().HasSynced,
					kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().HasSynced,
					kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().HasSynced,
				},
			},
			etcd.ObserveStorageURLs,
			network.ObserveRestrictedCIDRs,
			images.ObserveInternalRegistryHostname,
			authconfig.ObserveSessionSecret,
		),
	}

	operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Images().Informer().AddEventHandler(c.EventHandler())

	return c
}
