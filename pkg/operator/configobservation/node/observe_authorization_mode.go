package node

import (
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// There are scopes authorizer tests that fail if this order is changed.
// So this should not be sorted
var defaultAuthenticationModes = []string{
	"Scope",
	"SystemMasters",
	"RBAC",
	"Node",
}
var (
	authModeFlag  = "authorization-mode"
	apiServerArgs = "apiServerArguments"
	authModePath  = []string{apiServerArgs, authModeFlag}
)

type authorizationModeObserver struct {
	featureGateAccessor featuregates.FeatureGateAccess
	authModes           []string
}

func NewAuthorizationModeObserver(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&authorizationModeObserver{
		featureGateAccessor: featureGateAccessor,
	}).ObserveAuthorizationMode
}

// ObserveAuthorizationMode watches the featuregate configuration and generates the apiServerArguments.authorization-mode
// It currently hardcodes the default set and adds MinimumKubeletVersion if the feature is set to on.
func (o *authorizationModeObserver) ObserveAuthorizationMode(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains minimumKubeletVersion field.
		ret = configobserver.Pruned(ret, authModePath)
	}()

	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, append(errs, err)
	}

	ret = map[string]interface{}{}
	if err := AddAuthorizationModes(ret, featureGates.Enabled(features.FeatureGateMinimumKubeletVersion)); err != nil {
		return existingConfig, append(errs, err)
	}
	return ret, nil
}

// AddAuthorizationModes modifies the passed in config
// to add the "authorization-mode": "MinimumKubeletVersion" if the feature is on. If it's off, it
// removes it instead.
// This function assumes MinimumKubeletVersion auth mode isn't present by default,
// and should likely be removed when it is.
func AddAuthorizationModes(observedConfig map[string]interface{}, isMinimumKubeletVersionEnabled bool) error {
	modes := defaultAuthenticationModes
	if isMinimumKubeletVersionEnabled {
		modes = append(modes, ModeMinimumKubeletVersion)
	}

	unstructured.RemoveNestedField(observedConfig, authModePath...)
	return unstructured.SetNestedStringSlice(observedConfig, modes, authModePath...)
}
