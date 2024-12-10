package apienablement

import (
	"errors"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func staticObserver(cfg map[string]interface{}, errs []error) configobserver.ObserveConfigFunc {
	return func(configobserver.Listers, events.Recorder, map[string]interface{}) (map[string]interface{}, []error) {
		return cfg, errs
	}
}

func TestFeatureGateObserverWithRuntimeConfig(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		featureGates               featuregates.FeatureGateAccess
		groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion
		delegatedObserver          configobserver.ObserveConfigFunc
		existingConfig             map[string]interface{}
		expectedConfig             map[string]interface{}
		expectedErrors             bool
	}{
		{
			name:         "return existing config if initial feature gates not observed",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(nil, nil, make(chan struct{}), nil),
			existingConfig: map[string]interface{}{
				"prune": "me",
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
		},
		{
			name: "return existing config if error getting current feature gates",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(
				nil,
				nil,
				func() chan struct{} {
					c := make(chan struct{})
					close(c)
					return c
				}(),
				errors.New("test"),
			),
			existingConfig: map[string]interface{}{
				"prune": "me",
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedErrors: true,
		},
		{
			name:         "return config directly from feature gate observer if no runtime config applies",
			featureGates: featuregates.NewHardcodedFeatureGateAccess(nil, nil),
			delegatedObserver: staticObserver(
				map[string]interface{}{
					"prune": "me",
					"apiServerArguments": map[string]interface{}{
						"feature-gates": []interface{}{"foo"},
					},
				},
				nil,
			),
			existingConfig: map[string]interface{}{
				"prune": "me",
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"feature-gates": []interface{}{"foo"},
				},
			},
		},
		{
			name:                       "return existing config on failure to apply runtime-config",
			featureGates:               featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{"TestFeature"}, nil),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]schema.GroupVersion{"TestFeature": {{Version: "v6"}}},
			delegatedObserver: staticObserver(
				map[string]interface{}{
					"apiServerArguments": int64(42),
				},
				nil,
			),
			existingConfig: map[string]interface{}{
				"prune": "me",
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedErrors: true,
		},
		{
			name: "return config with runtime-config applied",
			featureGates: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{"TestEnabledFeature"},
				[]configv1.FeatureGateName{"TestDisabledFeature"},
			),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]schema.GroupVersion{
				"TestEnabledFeature":  {{Version: "v6"}},
				"TestDisabledFeature": {{Version: "v7"}},
			},
			delegatedObserver: staticObserver(
				map[string]interface{}{
					"apiServerArguments": map[string]interface{}{
						"feature-gates": []interface{}{"TestEnabledFeature=true"},
					},
				},
				nil,
			),
			existingConfig: map[string]interface{}{
				"prune": "me",
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"keep"},
					"runtime-config": []interface{}{"keep"},
				},
			},
			expectedConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"feature-gates":  []interface{}{"TestEnabledFeature=true"},
					"runtime-config": []interface{}{"v6=true"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, errs := newFeatureGateObserverWithRuntimeConfig(tc.delegatedObserver, tc.featureGates, tc.groupVersionsByFeatureGate)(nil, nil, tc.existingConfig)
			if diff := cmp.Diff(tc.expectedConfig, actual); diff != "" {
				t.Errorf("unexpected config:\n%s", diff)
			}
			if tc.expectedErrors && len(errs) == 0 {
				t.Errorf("expected errors but got none")
			}
			if !tc.expectedErrors && len(errs) > 0 {
				t.Errorf("unexpecteded errors: %v", errs)
			}
		})
	}
}

func TestGroupVersionsByFeatureGate(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		kubeVersion                semver.Version
		groupVersionsByFeatureGate map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion
		expectedGroupVersions      map[configv1.FeatureGateName][]schema.GroupVersion
		expectErrors               bool
	}{
		{
			name:        "partial from/to",
			kubeVersion: semver.MustParse("1.30.0"),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion{
				"ValidatingAdmissionPolicy": {{GroupVersion: schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"}}},
				"DynamicResourceAllocation": {
					{KubeVersionRange: semver.MustParseRange("< 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
				},
			},
			expectedGroupVersions: map[configv1.FeatureGateName][]schema.GroupVersion{
				"ValidatingAdmissionPolicy": {{Group: "admissionregistration.k8s.io", Version: "v1beta1"}},
				"DynamicResourceAllocation": {{Group: "resource.k8s.io", Version: "v1alpha2"}},
			},
		},
		{
			name:        "resolves newer API",
			kubeVersion: semver.MustParse("1.31.0"),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion{
				"DynamicResourceAllocation": {
					{KubeVersionRange: semver.MustParseRange("< 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
				},
			},
			expectedGroupVersions: map[configv1.FeatureGateName][]schema.GroupVersion{
				"DynamicResourceAllocation": {{Group: "resource.k8s.io", Version: "v1alpha3"}},
			},
		},
		{
			name:        "resolves minor versions API",
			kubeVersion: semver.MustParse("1.31.15"),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion{
				"DynamicResourceAllocation": {
					{KubeVersionRange: semver.MustParseRange("< 1.31.15"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.31.15"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
				},
			},
			expectedGroupVersions: map[configv1.FeatureGateName][]schema.GroupVersion{
				"DynamicResourceAllocation": {{Group: "resource.k8s.io", Version: "v1alpha3"}},
			},
		},
		{
			name:        "no intersection resolves to empty",
			kubeVersion: semver.MustParse("1.31.15"),
			groupVersionsByFeatureGate: map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion{
				"DynamicResourceAllocation": {
					{KubeVersionRange: semver.MustParseRange("< 1.31.14"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.31.16"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
				},
			},
			expectedGroupVersions: map[configv1.FeatureGateName][]schema.GroupVersion{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := getGroupVersionByFeatureGate(tc.groupVersionsByFeatureGate, tc.kubeVersion)
			if diff := cmp.Diff(tc.expectedGroupVersions, actual); diff != "" {
				t.Errorf("unexpected group versions:\n%s", diff)
			}
			if tc.expectErrors && err == nil {
				t.Errorf("expected errors but got none")
			}
			if !tc.expectErrors && err != nil {
				t.Errorf("unexpecteded errors: %v", err)
			}
		})
	}
}
