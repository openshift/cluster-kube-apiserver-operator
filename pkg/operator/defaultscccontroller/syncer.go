package defaultscccontroller

import (
	securityv1listers "github.com/openshift/client-go/security/listers/security/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/types"
)

type Syncer struct {
	lister   securityv1listers.SecurityContextConstraintsLister
	recorder events.Recorder
}

func (s *Syncer) Sync(key types.NamespacedName) error {
	return nil
}
