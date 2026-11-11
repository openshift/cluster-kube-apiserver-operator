package auth

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type FeatureGateLister interface {
	FeatureGateAccessor() featuregates.FeatureGateAccess
}

var configPath = []string{"admission", "pluginConfig", "PodSecurity", "configuration", "defaults"}

// SetPodSecurityAdmissionToPrivileged sets the PSA defaults to privileged:
// nothing is rejected and nothing is reported.
func SetPodSecurityAdmissionToPrivileged(config map[string]any) error {
	psaPrivileged := map[string]any{
		"enforce":         "privileged",
		"enforce-version": "latest",
		"audit":           "privileged",
		"audit-version":   "latest",
		"warn":            "privileged",
		"warn-version":    "latest",
	}

	unstructured.RemoveNestedField(config, configPath...)
	if err := unstructured.SetNestedMap(config, psaPrivileged, configPath...); err != nil {
		return fmt.Errorf("failed to set PodSecurity to privileged: %w", err)
	}

	return nil
}

// SetPodSecurityAdmissionToEnforcePrivilegedReportRestricted sets the PSA
// defaults to reject nothing, but to warn and audit at restricted.
func SetPodSecurityAdmissionToEnforcePrivilegedReportRestricted(config map[string]any) error {
	psaEnforcePrivileged := map[string]any{
		"enforce":         "privileged",
		"enforce-version": "latest",
		"audit":           "restricted",
		"audit-version":   "latest",
		"warn":            "restricted",
		"warn-version":    "latest",
	}

	unstructured.RemoveNestedField(config, configPath...)
	if err := unstructured.SetNestedMap(config, psaEnforcePrivileged, configPath...); err != nil {
		return fmt.Errorf("failed to set PodSecurity to enforce privileged and report restricted: %w", err)
	}

	return nil
}

func NewObservePodSecurityAdmissionEnforcementFunc(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&psaEnforcement{
		featureGateAccessor: featureGateAccessor,
	}).ObservePodSecurityAdmissionEnforcement
}

type psaEnforcement struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

// ObservePodSecurityAdmissionEnforcement sets the kube-apiserver's PSA defaults.
// It must never leave them unset: defaultconfig.yaml ships placeholders that the
// kube-apiserver refuses to start with, in this release and in rollback targets.
func (o *psaEnforcement) ObservePodSecurityAdmissionEnforcement(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]any) (ret map[string]any, _ []error) {
	return observePodSecurityAdmissionEnforcement(o.featureGateAccessor, recorder, existingConfig)
}

func observePodSecurityAdmissionEnforcement(featureGateAccessor featuregates.FeatureGateAccess, recorder events.Recorder, existingConfig map[string]any) (ret map[string]any, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, configPath)
	}()

	errs := []error{}

	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		// if we haven't observed featuregates yet, return the existing
		return existingConfig, nil
	}

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, append(errs, err)
	}

	observedConfig := map[string]any{}
	switch {
	case !featureGates.Enabled(features.FeatureGateOpenShiftPodSecurityAdmission):
		if err := SetPodSecurityAdmissionToEnforcePrivilegedReportRestricted(observedConfig); err != nil {
			return existingConfig, append(errs, err)
		}
	default:
		// Deliberately privileged with the gate on: PSA is moving to the APIServer config.
		if err := SetPodSecurityAdmissionToPrivileged(observedConfig); err != nil {
			return existingConfig, append(errs, err)
		}
	}

	return observedConfig, errs
}
