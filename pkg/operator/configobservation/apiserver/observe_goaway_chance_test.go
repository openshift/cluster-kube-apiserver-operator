package apiserver

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveGoawayChance(t *testing.T) {
	scenarios := []struct {
		name                  string
		existingKubeAPIConfig map[string]interface{}
		expectedKubeAPIConfig map[string]interface{}
		controlPlaneTopology  configv1.TopologyMode
	}{

		// scenario 1 - HA unset
		{
			name:                 "ha: defaults to 0.001",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.001"},
			}},
		},

		// scenario 3 - HA, update not required
		{
			name:                 "ha: update not required",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.001"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.001"},
			}},
		},

		// scenario 4 - HA, update required
		{
			name:                 "ha: update required",
			controlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.2"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.001"},
			}},
		},

		// scenario 5 - SNO
		{
			name:                 "sno: defaults to 0",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0"},
			}},
		},

		// scenario 6 - SNO, update required
		{
			name:                 "sno: update required",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0.001"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0"},
			}},
		},

		// scenario 7 - SNO, update not required
		{
			name:                 "sno: update not required",
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			existingKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0"},
			}},
			expectedKubeAPIConfig: map[string]interface{}{"apiServerArguments": map[string]interface{}{
				"goaway-chance": []interface{}{"0"},
			}},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("", clock.RealClock{})
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			infrastructureIndexer.Add(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.InfrastructureStatus{ControlPlaneTopology: scenario.controlPlaneTopology},
			})
			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infrastructureIndexer),
			}

			// act
			observedKubeAPIConfig, err := ObserveGoawayChance(listers, eventRecorder, scenario.existingKubeAPIConfig)

			// validate
			if len(err) > 0 {
				t.Fatal(err)
			}
			if diff := cmp.Diff(scenario.expectedKubeAPIConfig, observedKubeAPIConfig); diff != "" {
				t.Fatalf("unexpected configuration, diff = %s", diff)
			}
		})
	}
}

func TestObserveGoawayChanceErrors(t *testing.T) {
	scenarios := []struct {
		name             string
		infraIndexerFunc func(cache.Indexer)
		expectedErrs     []error
	}{
		{
			name: "happy path",
			infraIndexerFunc: func(indexer cache.Indexer) {
				indexer.Add(&configv1.Infrastructure{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status:     configv1.InfrastructureStatus{ControlPlaneTopology: configv1.HighlyAvailableTopologyMode},
				})
			},
			expectedErrs: nil,
		},
		{
			name:             "no cluster infra",
			infraIndexerFunc: nil,
			expectedErrs:     nil,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("", clock.RealClock{})
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if scenario.infraIndexerFunc != nil {
				scenario.infraIndexerFunc(infrastructureIndexer)
			}
			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infrastructureIndexer),
			}

			// act
			_, errs := ObserveGoawayChance(listers, eventRecorder, map[string]interface{}{})

			// validate
			if diff := cmp.Diff(scenario.expectedErrs, errs); diff != "" {
				t.Fatalf("unexpected errs, diff = %s", diff)
			}
		})
	}
}
