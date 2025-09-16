package apienablement

import (
	"fmt"
	"sort"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
)

var defaultGroupVersionsByFeatureGate = map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion{
	"MutatingAdmissionPolicy": {
		{KubeVersionRange: semver.MustParseRange("< 1.34.0"), GroupVersion: schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1alpha1"}},
		{KubeVersionRange: semver.MustParseRange(">= 1.34.0"), GroupVersion: schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"}},
	},
	"DynamicResourceAllocation": {
		{KubeVersionRange: semver.MustParseRange("< 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
		{KubeVersionRange: semver.MustParseRange(">= 1.31.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
		{KubeVersionRange: semver.MustParseRange(">= 1.32.0"), GroupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1beta1"}},
	},
	"VolumeAttributesClass": {{GroupVersion: schema.GroupVersion{Group: "storage.k8s.io", Version: "v1beta1"}}},
}

type groupVersionByOpenshiftVersion struct {
	schema.GroupVersion
	KubeVersionRange semver.Range
}

func getGroupVersionByFeatureGate(groupVersionsByFeatureGate map[configv1.FeatureGateName][]groupVersionByOpenshiftVersion, kubeVersion semver.Version) (map[configv1.FeatureGateName][]schema.GroupVersion, error) {
	result := make(map[configv1.FeatureGateName][]schema.GroupVersion, len(groupVersionsByFeatureGate))
	groupByVersions := map[string][]string{}
	for featureGate, APIGroups := range groupVersionsByFeatureGate {
		for _, group := range APIGroups {
			if group.KubeVersionRange == nil || group.KubeVersionRange(kubeVersion) {
				groupByVersions[group.Group] = append(groupByVersions[group.Group], group.Version)
				result[featureGate] = append(result[featureGate], group.GroupVersion)
			}
		}
	}
	return result, nil
}

func GetDefaultGroupVersionByFeatureGate(kubeVersion semver.Version) (map[configv1.FeatureGateName][]schema.GroupVersion, error) {
	return getGroupVersionByFeatureGate(defaultGroupVersionsByFeatureGate, kubeVersion)
}

var (
	featureGatesPath  = []string{"apiServerArguments", "feature-gates"}
	runtimeConfigPath = []string{"apiServerArguments", "runtime-config"}
)

// NewFeatureGateObserverWithRuntimeConfig returns a config observation function that observes
// feature gates and sets the --feature-gates and --runtime-config options accordingly. Since a
// mismatch between these two options can result in an unstable config, the observed value for
// either will only be set if both can be successfully set. Otherwise, the existing config is
// returned pruned but otherwise unmodified.
func NewFeatureGateObserverWithRuntimeConfig(featureWhitelist sets.Set[configv1.FeatureGateName], featureBlacklist sets.Set[configv1.FeatureGateName], featureGateAccessor featuregates.FeatureGateAccess, groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion) configobserver.ObserveConfigFunc {

	featureGateObserver := featuregates.NewObserveFeatureFlagsFunc(
		featureWhitelist,
		featureBlacklist,
		featureGatesPath,
		featureGateAccessor,
	)

	return newFeatureGateObserverWithRuntimeConfig(featureGateObserver, featureGateAccessor, groupVersionsByFeatureGate)
}

func newFeatureGateObserverWithRuntimeConfig(featureGateObserver configobserver.ObserveConfigFunc, featureGateAccessor featuregates.FeatureGateAccess, groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion) configobserver.ObserveConfigFunc {
	return func(listers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
		defer func() {
			observedConfig = configobserver.Pruned(observedConfig, featureGatesPath, runtimeConfigPath)
		}()

		if !featureGateAccessor.AreInitialFeatureGatesObserved() {
			return existingConfig, nil
		}

		featureGates, err := featureGateAccessor.CurrentFeatureGates()
		if err != nil {
			return existingConfig, []error{err}
		}

		observedConfig, errs = featureGateObserver(listers, recorder, existingConfig)

		runtimeConfig := RuntimeConfigFromFeatureGates(featureGates, groupVersionsByFeatureGate)
		if len(runtimeConfig) == 0 {
			return observedConfig, errs
		}

		if err := unstructured.SetNestedStringSlice(observedConfig, runtimeConfig, runtimeConfigPath...); err != nil {
			// The new feature gate config is broken without its required APIs.
			return existingConfig, append(errs, err)
		}

		return observedConfig, errs
	}
}

func RuntimeConfigFromFeatureGates(featureGates featuregates.FeatureGate, groupVersionsByFeatureGate map[configv1.FeatureGateName][]schema.GroupVersion) []string {
	var entries []string
	for name, gvs := range groupVersionsByFeatureGate {
		if !featureGates.Enabled(name) {
			continue
		}
		for _, gv := range gvs {
			entries = append(entries, fmt.Sprintf("%s=true", gv.String()))
		}
	}
	sort.Strings(entries)
	return entries
}
