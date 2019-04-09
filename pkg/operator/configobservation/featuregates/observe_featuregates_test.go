package featuregates

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveFeatureFlags(t *testing.T) {
	tests := []struct {
		name string

		configValue    configv1.FeatureSet
		expectedResult []string
	}{
		{
			name:        "default",
			configValue: configv1.Default,
			expectedResult: []string{
				"ExperimentalCriticalPodAnnotation=true",
				"RotateKubeletServerCertificate=true",
				"SupportPodPidsLimit=true",
				"LocalStorageCapacityIsolation=false",
			},
		},
		{
			name:        "techpreview",
			configValue: configv1.TechPreviewNoUpgrade,
			expectedResult: []string{
				"ExperimentalCriticalPodAnnotation=true",
				"RotateKubeletServerCertificate=true",
				"SupportPodPidsLimit=true",
				"CSIBlockVolume=true",
				"LocalStorageCapacityIsolation=false",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			indexer.Add(&configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.FeatureGateSpec{
					FeatureSet: tc.configValue,
				},
			})
			listers := configobservation.Listers{
				FeatureGateLister: configlistersv1.NewFeatureGateLister(indexer),
			}
			eventRecorder := events.NewInMemoryRecorder("")

			initialExistingConfig := map[string]interface{}{}

			observed, errs := ObserveFeatureFlags(listers, eventRecorder, initialExistingConfig)
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			actual, _, err := unstructured.NestedStringSlice(observed, configPath...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(tc.expectedResult, actual) {
				t.Errorf("%v", actual)
			}
		})
	}
}
