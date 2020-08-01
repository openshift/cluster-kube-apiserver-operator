package configobservation

import (
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

var _ cloudprovider.InfrastructureLister = Listers{}

type Listers struct {
	APIServerLister_      configlistersv1.APIServerLister
	AuthConfigLister      configlistersv1.AuthenticationLister
	FeatureGateLister_    configlistersv1.FeatureGateLister
	InfrastructureLister_ configlistersv1.InfrastructureLister
	ImageConfigLister     configlistersv1.ImageLister
	NetworkLister         configlistersv1.NetworkLister
	OAuthLister_          configlistersv1.OAuthLister
	ProxyLister_          configlistersv1.ProxyLister
	SchedulerLister       configlistersv1.SchedulerLister

	OpenshiftEtcdEndpointsLister corelistersv1.EndpointsLister
	ConfigmapLister              corelistersv1.ConfigMapLister
	SecretLister_                corelistersv1.SecretLister
	ConfigSecretLister_          corelistersv1.SecretLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
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

func (l Listers) OAuthLister() configlistersv1.OAuthLister {
	return l.OAuthLister_
}

func (l Listers) SecretLister() corelistersv1.SecretLister {
	return l.SecretLister_
}

func (l Listers) ConfigSecretLister() corelistersv1.SecretLister {
	return l.ConfigSecretLister_
}

func (l Listers) ProxyLister() configlistersv1.ProxyLister {
	return l.ProxyLister_
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}

func (l Listers) ConfigMapLister() corelistersv1.ConfigMapLister {
	return l.ConfigmapLister
}
