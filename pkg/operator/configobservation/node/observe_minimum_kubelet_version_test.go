package node

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestObserveKubeletMinimumVersion(t *testing.T) {
	type Test struct {
		name                  string
		existingConfig        map[string]interface{}
		expectedConfig        map[string]interface{}
		minimumKubeletVersion string
		featureOn             bool
	}
	tests := []Test{
		{
			name:                  "feature off",
			existingConfig:        map[string]interface{}{},
			expectedConfig:        map[string]interface{}{},
			minimumKubeletVersion: "1.30.0",
			featureOn:             false,
		},
		{
			name:                  "empty minimumKubeletVersion",
			expectedConfig:        map[string]interface{}{},
			minimumKubeletVersion: "",
			featureOn:             true,
		},
		{
			name: "set minimumKubeletVersion",
			expectedConfig: map[string]interface{}{
				"minimumKubeletVersion": string("1.30.0"),
			},
			minimumKubeletVersion: "1.30.0",
			featureOn:             true,
		},
		{
			name: "existing minimumKubeletVersion",
			existingConfig: map[string]interface{}{
				"minimumKubeletVersion": string("1.29.0"),
			},
			expectedConfig: map[string]interface{}{
				"minimumKubeletVersion": string("1.30.0"),
			},
			minimumKubeletVersion: "1.30.0",
			featureOn:             true,
		},
		{
			name:           "existing minimumKubeletVersion unset",
			expectedConfig: map[string]any{},
			existingConfig: map[string]interface{}{
				"minimumKubeletVersion": string("1.29.0"),
			},
			minimumKubeletVersion: "",
			featureOn:             true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("", clocktesting.NewFakePassiveClock(time.Now()))
			configNodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			configNodeIndexer.Add(&configv1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.NodeSpec{MinimumKubeletVersion: test.minimumKubeletVersion},
			})
			listers := configobservation.Listers{
				NodeLister_: configlistersv1.NewNodeLister(configNodeIndexer),
			}

			fg := featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{features.FeatureGateMinimumKubeletVersion}, []configv1.FeatureGateName{})
			if !test.featureOn {
				fg = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateMinimumKubeletVersion})
			}

			// act
			actualObservedConfig, errs := NewMinimumKubeletVersionObserver(fg)(listers, eventRecorder, test.existingConfig)

			// validate
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if diff := cmp.Diff(test.expectedConfig, actualObservedConfig); diff != "" {
				t.Fatalf("unexpected configuration, diff = %v", diff)
			}
		})
	}
}
