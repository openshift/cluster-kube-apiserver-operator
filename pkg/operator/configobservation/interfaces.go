package configobservation

import (
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

// must satisfy configobserver.Listers interface which is passed to the config observer functions. It is expected to be hard-cast to the "correct" type.
var _ configobserver.Listers = Listers{}

// Listers a struct that holds a bunch of listers for various resources
type Listers struct {
	APIServerLister      configlistersv1.APIServerLister
	AuthConfigLister     configlistersv1.AuthenticationLister
	FeatureGateLister    configlistersv1.FeatureGateLister
	InfrastructureLister configlistersv1.InfrastructureLister
	ImageConfigLister    configlistersv1.ImageLister
	NetworkLister        configlistersv1.NetworkLister
	ProxyLister          configlistersv1.ProxyLister
	SchedulerLister      configlistersv1.SchedulerLister

	OpenshiftEtcdEndpointsLister corelistersv1.EndpointsLister
	ConfigmapLister              corelistersv1.ConfigMapLister
	SecretLister                 corelistersv1.SecretLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}
