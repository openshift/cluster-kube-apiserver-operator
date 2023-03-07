package auth

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
)

type FeatureGateLister interface {
	FeatureGateLister() configlistersv1.FeatureGateLister
}

var configPath = []string{"admission", "pluginConfig", "PodSecurity", "configuration", "defaults"}

// We want this:
/*
	admission:
		pluginConfig:
			PodSecurity:
				configuration:
					kind: PodSecurityConfiguration
					apiVersion: pod-security.admission.config.k8s.io/v1beta1
					defaults:
						enforce: "restricted"
						enforce-version: "latest"
*/
func SetPodSecurityAdmissionToEnforceRestricted(config map[string]interface{}) error {
	psaEnforceRestricted := map[string]interface{}{
		"enforce":         "restricted",
		"enforce-version": "latest",
		"audit":           "restricted",
		"audit-version":   "latest",
		"warn":            "restricted",
		"warn-version":    "latest",
	}

	unstructured.RemoveNestedField(config, configPath...)
	if err := unstructured.SetNestedMap(config, psaEnforceRestricted, configPath...); err != nil {
		return fmt.Errorf("failed to set PodSecurity to enforce restricted: %w", err)
	}

	return nil
}

func SetPodSecurityAdmissionToEnforcePrivileged(config map[string]interface{}) error {
	psaEnforceRestricted := map[string]interface{}{
		"enforce":         "privileged",
		"enforce-version": "latest",
		"audit":           "restricted",
		"audit-version":   "latest",
		"warn":            "restricted",
		"warn-version":    "latest",
	}

	unstructured.RemoveNestedField(config, configPath...)
	if err := unstructured.SetNestedMap(config, psaEnforceRestricted, configPath...); err != nil {
		return fmt.Errorf("failed to set PodSecurity to enforce restricted: %w", err)
	}

	return nil
}

// ObserveFeatureFlags fills in --feature-flags for the kube-apiserver
func ObservePodSecurityAdmissionEnforcement(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	listers := genericListers.(FeatureGateLister)
	errs := []error{}

	featureGate, err := listers.FeatureGateLister().Get("cluster")
	// if we have no featuregate, then the installer and MCO probably still have way to reconcile certain custom resources
	// we will assume that this means the same as default and hope for the best
	if apierrors.IsNotFound(err) {
		featureGate = &configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.Default,
				},
			},
		}
	} else if err != nil {
		return existingConfig, append(errs, err)
	}

	return observePodSecurityAdmissionEnforcement(featureGate, recorder, existingConfig)
}

func observePodSecurityAdmissionEnforcement(featureGate *configv1.FeatureGate, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, configPath)
	}()

	errs := []error{}

	enabled, _, err := featuregates.FeaturesGatesFromFeatureSets(featureGate)
	if err != nil {
		return existingConfig, append(errs, err)
	}

	observedConfig := map[string]interface{}{}
	switch {
	case sets.NewString(enabled...).Has("OpenShiftPodSecurityAdmission"):
		if err := SetPodSecurityAdmissionToEnforceRestricted(observedConfig); err != nil {
			return existingConfig, append(errs, err)
		}
	default:
		if err := SetPodSecurityAdmissionToEnforcePrivileged(observedConfig); err != nil {
			return existingConfig, append(errs, err)
		}
	}

	return observedConfig, errs
}
