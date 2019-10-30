package apiserver

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/tools/cache"
)

func TestObserveAdditionalCORSAllowedOrigins(t *testing.T) {
	existingConfig := map[string]interface{}{
		"corsAllowedOrigins": []interface{}{
			`(?i)//my\.subdomain\.domain\.com(:|\z)`,
		},
	}

	testCases := []struct {
		name     string
		config   *configv1.APIServer
		existing map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "NoAPIServerConfig",
			config:   nil,
			existing: existingConfig,
			expected: map[string]interface{}{
				"corsAllowedOrigins": []interface{}{
					`//127\.0\.0\.1(:|$)`,
					`//localhost(:|$)`,
				},
			},
		},
		{
			name:     "NoAdditionalCORSAllowedOrigins",
			config:   newAPIServerConfig(),
			existing: existingConfig,
			expected: map[string]interface{}{
				"corsAllowedOrigins": []interface{}{
					`//127\.0\.0\.1(:|$)`,
					`//localhost(:|$)`,
				},
			},
		},
		{
			name:     "HappyPath",
			config:   newAPIServerConfig(withAdditionalCORSAllowedOrigins([]string{`(?i)//happy\.domain\.cz(:|\z)`})),
			existing: existingConfig,
			expected: map[string]interface{}{
				"corsAllowedOrigins": []interface{}{
					`(?i)//happy\.domain\.cz(:|\z)`,
					`//127\.0\.0\.1(:|$)`,
					`//localhost(:|$)`,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tc.config != nil {
				if err := indexer.Add(tc.config); err != nil {
					t.Fatal(err)
				}
			}
			synced := map[string]string{}
			listers := configobservation.Listers{
				APIServerLister_: configlistersv1.NewAPIServerLister(indexer),
				ResourceSync:     &mockResourceSyncer{t: t, synced: synced},
			}
			result, errs := ObserveAdditionalCORSAllowedOrigins(listers, events.NewInMemoryRecorder(t.Name()), tc.existing)
			if len(errs) > 0 {
				t.Errorf("Expected 0 errors, got %v.", len(errs))
			}
			if !equality.Semantic.DeepEqual(tc.expected, result) {
				t.Errorf("result does not match expected config: %s", diff.ObjectDiff(tc.expected, result))
			}
		})
	}
}

func withAdditionalCORSAllowedOrigins(cors []string) func(*configv1.APIServer) {
	return func(apiserver *configv1.APIServer) {
		apiserver.Spec.AdditionalCORSAllowedOrigins = append(apiserver.Spec.AdditionalCORSAllowedOrigins, cors...)
	}
}
