package node

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
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
			listers := testLister{
				nodeLister: configlistersv1.NewNodeLister(configNodeIndexer),
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

func TestSetAPIServerArgumentsToEnforceMinimumKubeletVersion(t *testing.T) {
	for _, on := range []bool{false, true} {
		expectedSet := []any{"Node", "RBAC", "Scope", "SystemMasters"}
		if on {
			expectedSet = append([]any{ModeMinimumKubeletVersion}, expectedSet...)
		}
		for _, tc := range []struct {
			name           string
			existingConfig map[string]interface{}
			expectedConfig map[string]interface{}
		}{
			{
				name: "should not fail if apiServerArguments not present",
				existingConfig: map[string]interface{}{
					"fakeconfig": "fake",
				},
				expectedConfig: map[string]interface{}{
					"fakeconfig":         "fake",
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if authorization-mode not present",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"fake": []any{"fake"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"fake": []any{"fake"}, "authorization-mode": expectedSet},
				},
			},
			{
				name: "should clobber value if not expected",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": []any{"fake"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if MinimumKubeletVersion already present",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": []any{"MinimumKubeletVersion"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if apiServerArguments not present",
				existingConfig: map[string]interface{}{
					"fakeconfig": "fake",
				},
				expectedConfig: map[string]interface{}{
					"fakeconfig":         "fake",
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
		} {
			name := tc.name + " when feature is "
			if on {
				name += "on"
			} else {
				name += "off"
			}
			t.Run(name, func(t *testing.T) {
				if err := SetAPIServerArgumentsToEnforceMinimumKubeletVersion(tc.existingConfig, on); err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(tc.expectedConfig, tc.existingConfig); diff != "" {
					t.Errorf("unexpected config:\n%s", diff)
				}
			})
		}
	}
}
