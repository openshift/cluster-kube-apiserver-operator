package apiserver

import (
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"k8s.io/client-go/tools/cache"
)

type APIServerLister interface {
	APIServerLister() configlistersv1.APIServerLister
	PreRunHasSynced() []cache.InformerSynced
}
