package apienablement

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
)

type GroupVersion struct {
	kubeVersion  uint64
	groupVersion schema.GroupVersion
}

var DefaultGroupVersionsByFeatureGate = map[configv1.FeatureGateName][]GroupVersion{
	"ValidatingAdmissionPolicy": {
		{groupVersion: schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"}},
	},
	"DynamicResourceAllocation": {
		{kubeVersion: 30, groupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha2"}},
		{kubeVersion: 31, groupVersion: schema.GroupVersion{Group: "resource.k8s.io", Version: "v1alpha3"}},
	},
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
func NewFeatureGateObserverWithRuntimeConfig(featureWhitelist sets.Set[configv1.FeatureGateName], featureBlacklist sets.Set[configv1.FeatureGateName], featureGateAccessor featuregates.FeatureGateAccess, groupVersionsByFeatureGate map[configv1.FeatureGateName][]GroupVersion) configobserver.ObserveConfigFunc {

	featureGateObserver := featuregates.NewObserveFeatureFlagsFunc(
		featureWhitelist,
		featureBlacklist,
		featureGatesPath,
		featureGateAccessor,
	)

	return newFeatureGateObserverWithRuntimeConfig(featureGateObserver, featureGateAccessor, groupVersionsByFeatureGate)
}

func newFeatureGateObserverWithRuntimeConfig(featureGateObserver configobserver.ObserveConfigFunc, featureGateAccessor featuregates.FeatureGateAccess, groupVersionsByFeatureGate map[configv1.FeatureGateName][]GroupVersion) configobserver.ObserveConfigFunc {
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

		apiServerVersion := semver.MustParse(status.VersionForOperandFromEnv())
		runtimeConfig := RuntimeConfigFromFeatureGates(featureGates, groupVersionsByFeatureGate, apiServerVersion)
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

func RuntimeConfigFromFeatureGates(featureGates featuregates.FeatureGate, groupVersionsByFeatureGate map[configv1.FeatureGateName][]GroupVersion, apiServerVersion semver.Version) []string {
	var entries []string
	for name, gvs := range groupVersionsByFeatureGate {
		if !featureGates.Enabled(name) {
			continue
		}
		for _, gv := range gvs {
			if gv.kubeVersion == 0 {
				// Kube version not specified: then load the API
				entries = append(entries, fmt.Sprintf("%s=true", gv.groupVersion.String()))
			} else if apiServerVersion.Minor == gv.kubeVersion {
				// Kube version specified: needs to match
				entries = append(entries, fmt.Sprintf("%s=true", gv.groupVersion.String()))
			} else {
				// do not load api. kube version is set, but didn't match current kube release.
			}
		}
	}
	sort.Strings(entries)
	return entries
}
