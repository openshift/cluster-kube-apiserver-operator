package certrotationcontroller

import (
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"testing"
)

func TestServiceHostNameFunc(t *testing.T) {
	scenarios := []struct {
		name          string
		objects       []runtime.Object
		expectedError error
	}{
		{
			"network config status not available",
			[]runtime.Object{
				fakeNetwork(false),
			},
			fmt.Errorf("empty networkConfig ServiceNetwork, can't generate cert"),
		},
		{
			"happy with network status network ServiceNetwork",
			[]runtime.Object{
				fakeNetwork(true),
			},
			nil,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, obj := range scenario.objects {
				if err := indexer.Add(obj); err != nil {
					require.NoError(t, err)
				}
			}
			controller := CertRotationController{
				networkLister:  configv1listers.NewNetworkLister(indexer),
				serviceNetwork: &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},
			}
			err := controller.syncServiceHostnames()
			require.Equal(t, err, scenario.expectedError)
		})
	}
}

func fakeNetwork(hasServiceNetwork bool) *configv1.Network {
	var serviceNetwork []string
	if hasServiceNetwork {
		serviceNetwork = []string{"10.0.1.0/24"}
	} else {
		serviceNetwork = []string{}
	}
	return &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status:     configv1.NetworkStatus{ServiceNetwork: serviceNetwork},
	}
}
