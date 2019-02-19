package configobservation

import (
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

type Listers struct {
	AuthConfigLister  configlistersv1.AuthenticationLister
	ImageConfigLister configlistersv1.ImageLister
	EndpointsLister   corelistersv1.EndpointsLister
	ConfigmapLister   corelistersv1.ConfigMapLister
	APIServerLister   configlistersv1.APIServerLister
	NetworkLister     configlistersv1.NetworkLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}
