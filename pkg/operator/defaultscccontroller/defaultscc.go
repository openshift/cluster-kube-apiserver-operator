package defaultscccontroller

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"github.com/openshift/api"
	securityv1 "github.com/openshift/api/security/v1"

	assets "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/defaultscc_assets"
)

// DefaultSCCCache maintains the list of default SecurityContextConstraints objects
// shipped wth an OpenShift cluster.
type DefaultSCCCache struct {
	orderedNames []string
	set          map[string]*securityv1.SecurityContextConstraints
}

// Get returns the SecurityContextConstraints object associated with the
// specified name. The returned SecurityContextConstraints should be deep copied
// before it is modified otherwise the integrity of the underlying cache will be impacted.
func (d *DefaultSCCCache) Get(name string) (scc *securityv1.SecurityContextConstraints, exists bool) {
	scc, exists = d.set[name]
	return
}

func (d *DefaultSCCCache) DefaultSCCNames() []string {
	return d.orderedNames
}

// NewDefaultSCCCache renders the assets associated with the default set of SCC and
// loads it into a cache (DefaultSCCCache)
func NewDefaultSCCCache() (cache *DefaultSCCCache, err error) {
	decoder, decoderErr := decoder()
	if decoderErr != nil {
		err = fmt.Errorf("failed to create decoder - %s", decoderErr.Error())
		return
	}

	names := make([]string, 0)
	set := make(map[string]*securityv1.SecurityContextConstraints)
	for _, name := range assets.AssetNames() {
		bytes, assetErr := assets.Asset(name)
		if assetErr != nil {
			return
		}

		object, _, decodeErr := decoder.Decode(bytes, nil, nil)
		if decodeErr != nil {
			err = fmt.Errorf("failed to decode SecurityContextConstraints from asset name=%s - %s", name, decodeErr.Error())
			return
		}

		scc, ok := object.(*securityv1.SecurityContextConstraints)
		if !ok {
			err = fmt.Errorf("obj is not SecurityContextConstraint type name=%s", name)
			return
		}

		_, exists := set[scc.GetName()]
		if exists {
			err = fmt.Errorf("SecurityContextConstraint already exists in set name=%s", scc.GetName())
			return
		}

		set[scc.GetName()] = scc
		names = append(names, scc.GetName())
	}

	// sort the names so that we guarantee an iteration with a certain order.
	sort.Strings(names)

	cache = &DefaultSCCCache{
		orderedNames: names,
		set:          set,
	}
	return
}

func decoder() (decoder runtime.Decoder, err error) {
	scheme := runtime.NewScheme()
	if err = api.Install(scheme); err != nil {
		return
	}

	factory := serializer.NewCodecFactory(scheme)

	decoder = factory.UniversalDeserializer()
	return
}
