package configobservation

import (
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorlistersv1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	libgoetcd "github.com/openshift/library-go/pkg/operator/configobserver/etcd"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

var _ cloudprovider.InfrastructureLister = Listers{}
var _ libgoetcd.ConfigMapLister = Listers{}

type Listers struct {
	APIServerLister_      configlistersv1.APIServerLister
	AuthConfigLister      configlistersv1.AuthenticationLister
	FeatureGateLister_    configlistersv1.FeatureGateLister
	InfrastructureLister_ configlistersv1.InfrastructureLister
	ImageConfigLister     configlistersv1.ImageLister
	NetworkLister         configlistersv1.NetworkLister
	NodeLister_           configlistersv1.NodeLister
	ProxyLister_          configlistersv1.ProxyLister
	SchedulerLister       configlistersv1.SchedulerLister

	ConfigmapLister_    corelistersv1.ConfigMapLister
	SecretLister_       corelistersv1.SecretLister
	ConfigSecretLister_ corelistersv1.SecretLister

	KubeAPIServerOperatorLister_ operatorlistersv1.KubeAPIServerLister
	KubeAPIServerOperatorClient  operatorv1client.KubeAPIServerInterface

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) KubeAPIServerOperatorLister() operatorlistersv1.KubeAPIServerLister {
	return l.KubeAPIServerOperatorLister_
}

func (l Listers) APIServerLister() configlistersv1.APIServerLister {
	return l.APIServerLister_
}

func (l Listers) FeatureGateLister() configlistersv1.FeatureGateLister {
	return l.FeatureGateLister_
}

func (l Listers) InfrastructureLister() configlistersv1.InfrastructureLister {
	return l.InfrastructureLister_
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}

func (l Listers) SecretLister() corelistersv1.SecretLister {
	return l.SecretLister_
}

func (l Listers) ConfigSecretLister() corelistersv1.SecretLister {
	return l.ConfigSecretLister_
}

func (l Listers) NodeLister() configlistersv1.NodeLister {
	return l.NodeLister_
}

func (l Listers) ProxyLister() configlistersv1.ProxyLister {
	return l.ProxyLister_
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}

func (l Listers) ConfigMapLister() corelistersv1.ConfigMapLister {
	return l.ConfigmapLister_
}
