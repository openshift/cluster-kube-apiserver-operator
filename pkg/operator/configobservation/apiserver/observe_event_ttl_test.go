package apiserver

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorlistersv1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestObserveEventTTL(t *testing.T) {
	scenarios := []struct {
		name                  string
		existingKubeAPIConfig map[string]interface{}
		expectedKubeAPIConfig map[string]interface{}
		eventTTLMinutes       int32
		featureOn             bool
	}{
		{
			name:                  "feature gate disabled",
			existingKubeAPIConfig: map[string]interface{}{},
			expectedKubeAPIConfig: map[string]interface{}{},
			eventTTLMinutes:       120,
			featureOn:             false,
		},
		{
			name: "feature gate disabled clears existing event-ttl",
			existingKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"120m"},
				},
			},
			expectedKubeAPIConfig: map[string]interface{}{},
			eventTTLMinutes:       0,
			featureOn:             false,
		},
		{
			name:                  "feature gate enabled, no event TTL set - use default from defaultconfig.yaml",
			existingKubeAPIConfig: map[string]interface{}{},
			expectedKubeAPIConfig: map[string]interface{}{},
			eventTTLMinutes:       0,
			featureOn:             true,
		},
		{
			name:                  "feature gate enabled, event TTL set to 60 minutes",
			existingKubeAPIConfig: map[string]interface{}{},
			expectedKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"60m"},
				},
			},
			eventTTLMinutes: 60,
			featureOn:       true,
		},
		{
			name:                  "feature gate enabled, event TTL set to 180 minutes (maximum)",
			existingKubeAPIConfig: map[string]interface{}{},
			expectedKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"180m"},
				},
			},
			eventTTLMinutes: 180,
			featureOn:       true,
		},
		{
			name:                  "feature gate enabled, event TTL set to 5 minutes (minimum)",
			existingKubeAPIConfig: map[string]interface{}{},
			expectedKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"5m"},
				},
			},
			eventTTLMinutes: 5,
			featureOn:       true,
		},
		{
			name: "feature gate enabled, update existing config",
			existingKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"120m"},
				},
			},
			expectedKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"90m"},
				},
			},
			eventTTLMinutes: 90,
			featureOn:       true,
		},
		{
			name: "feature gate enabled, no change needed",
			existingKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"120m"},
				},
			},
			expectedKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"120m"},
				},
			},
			eventTTLMinutes: 120,
			featureOn:       true,
		},
		{
			name: "feature gate enabled, set default event-ttl when set to 0",
			existingKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"120m"},
				},
			},
			expectedKubeAPIConfig: map[string]interface{}{},
			eventTTLMinutes:       0,
			featureOn:             true,
		},
		{
			name: "feature gate enabled, no change needed when already at default, returning empty",
			existingKubeAPIConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"event-ttl": []interface{}{"3h"},
				},
			},
			expectedKubeAPIConfig: map[string]interface{}{},
			eventTTLMinutes:       0,
			featureOn:             true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("", clocktesting.NewFakePassiveClock(time.Now()))
			kubeAPIServerIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

			// Add KubeAPIServer resource
			_ = kubeAPIServerIndexer.Add(&operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: operatorv1.KubeAPIServerSpec{
					EventTTLMinutes: scenario.eventTTLMinutes,
				},
			})

			listers := configobservation.Listers{
				KubeAPIServerOperatorLister_: operatorlistersv1.NewKubeAPIServerLister(kubeAPIServerIndexer),
			}

			// Set up feature gate accessor
			var fg featuregates.FeatureGateAccess
			if scenario.featureOn {
				fg = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{features.FeatureEventTTL}, []configv1.FeatureGateName{})
			} else {
				fg = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureEventTTL})
			}

			observer := NewObserveEventTTL(fg)
			observedKubeAPIConfig, errs := observer(listers, eventRecorder, scenario.existingKubeAPIConfig)

			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if diff := cmp.Diff(scenario.expectedKubeAPIConfig, observedKubeAPIConfig); diff != "" {
				t.Fatalf("unexpected configuration, diff = %s", diff)
			}
		})
	}
}
