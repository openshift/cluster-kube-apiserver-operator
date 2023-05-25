package apiserver

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveSendRetryAfterWhileNotReadyOnce(t *testing.T) {
	scenarios := []struct {
		name                    string
		validateKubeAPIConfigFn func(kubecontrolplanev1.KubeAPIServerConfig) error
		existingKubeAPIConfig   map[string]interface{}
		expectedKubeAPIConfig   map[string]interface{}
		controlPlaneTopology    configv1.TopologyMode
	}{

		// scenario 1 - HA unset
		{
			name:                 "ha: defaults to false",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"false"},
			}},
		},

		// scenario 3 - HA, update not required
		{
			name:                 "ha: update not required",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"false"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"false"},
			}},
		},

		// scenario 4 - HA, update required
		{
			name:                 "ha: update required",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"true"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"false"},
			}},
		},

		// scenario 5 - SNO
		{
			name:                 "ha: defaults to true",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"true"},
			}},
		},

		// scenario 6 - SNO, update required
		{
			name:                 "sno: update required",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"false"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"true"},
			}},
		},

		// scenario 7 - SNO, update not required
		{
			name:                 "sno: update not required",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"true"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"send-retry-after-while-not-ready-once": []interface{}{"true"},
			}},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("")
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			infrastructureIndexer.Add(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.InfrastructureStatus{ControlPlaneTopology: scenario.controlPlaneTopology},
			})
			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infrastructureIndexer),
			}

			// act
			observedKubeAPIConfig, err := ObserveSendRetryAfterWhileNotReadyOnce(listers, eventRecorder, scenario.existingKubeAPIConfig)

			// validate
			if len(err) > 0 {
				t.Fatal(err)
			}
			if !cmp.Equal(scenario.expectedKubeAPIConfig, observedKubeAPIConfig) {
				t.Fatalf("unexpected configuration, diff = %v", cmp.Diff(scenario.expectedKubeAPIConfig, observedKubeAPIConfig))
			}
		})
	}
}
