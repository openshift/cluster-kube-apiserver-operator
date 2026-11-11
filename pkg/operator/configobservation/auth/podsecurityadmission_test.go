package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	psapi "k8s.io/pod-security-admission/api"

	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/utils/clock"
)

func TestObservePodSecurityAdmissionEnforcement(t *testing.T) {
	// gate off
	enforcePrivilegedReportRestricted := map[string]any{
		"enforce": "privileged", "enforce-version": "latest",
		"audit": "restricted", "audit-version": "latest",
		"warn": "restricted", "warn-version": "latest",
	}
	// gate on
	privileged := map[string]any{
		"enforce": "privileged", "enforce-version": "latest",
		"audit": "privileged", "audit-version": "latest",
		"warn": "privileged", "warn-version": "latest",
	}
	// gate on, as older releases wrote it
	enforceRestricted := map[string]any{
		"enforce": "restricted", "enforce-version": "latest",
		"audit": "restricted", "audit-version": "latest",
		"warn": "restricted", "warn-version": "latest",
	}
	sentinel := map[string]any{"foo": "bar"}

	// observedConfig nests the defaults where the observer writes them.
	observedConfig := func(defaults map[string]any) map[string]any {
		config := map[string]any{}
		require.NoError(t, unstructured.SetNestedMap(config, defaults, "admission", "pluginConfig", "PodSecurity", "configuration", "defaults"))
		return config
	}

	enabledFeatureSet := featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{features.FeatureGateOpenShiftPodSecurityAdmission}, []configv1.FeatureGateName{})
	disabledFeatureSet := featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateOpenShiftPodSecurityAdmission})

	for _, tc := range []struct {
		name                string
		existing            map[string]any
		featureGateAccessor featuregates.FeatureGateAccess
		expectedErr         string
		expected            map[string]any
	}{
		{
			name:                "enabled",
			existing:            enforcePrivilegedReportRestricted,
			featureGateAccessor: enabledFeatureSet,
			expectedErr:         "",
			expected:            privileged,
		},
		{
			name:                "enabled, restricted from older releases becomes privileged",
			existing:            enforceRestricted,
			featureGateAccessor: enabledFeatureSet,
			expectedErr:         "",
			expected:            privileged,
		},
		{
			name:                "disabled",
			existing:            privileged,
			featureGateAccessor: disabledFeatureSet,
			expectedErr:         "",
			expected:            enforcePrivilegedReportRestricted,
		},
		{
			name:                "initial feature gates not observed",
			existing:            sentinel,
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccessForTesting(nil, nil, make(chan struct{}), nil),
			expected:            sentinel,
		},
		{
			name:     "error reading current feature gates",
			existing: sentinel,
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccessForTesting(
				nil,
				nil,
				func() chan struct{} {
					c := make(chan struct{})
					close(c)
					return c
				}(),
				errors.New("test error"),
			),
			expected:    sentinel,
			expectedErr: "test error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest", clock.RealClock{})

			actual, errs := observePodSecurityAdmissionEnforcement(tc.featureGateAccessor, testRecorder, observedConfig(tc.existing))

			switch {
			case len(errs) == 0 && len(tc.expectedErr) == 0:
			case len(errs) == 0 && len(tc.expectedErr) > 0:
				t.Fatalf("missing err: %v", tc.expectedErr)

			case len(errs) > 0 && len(tc.expectedErr) == 0:
				t.Fatal(errs)
			case len(errs) > 0 && len(tc.expectedErr) > 0 && !strings.Contains(utilerrors.NewAggregate(errs).Error(), tc.expectedErr):
				t.Fatalf("missing err: %v in \n%v", tc.expectedErr, errs)
			}

			require.Equal(t, observedConfig(tc.expected), actual)
		})
	}
}

// A placeholder left in defaultconfig.yaml keeps the kube-apiserver from starting.
func TestObservePodSecurityAdmissionEnforcementReplacesPlaceholders(t *testing.T) {
	defaultConfigJSON, err := kyaml.ToJSON(bindata.MustAsset("assets/config/defaultconfig.yaml"))
	require.NoError(t, err)
	defaultConfig := map[string]any{}
	require.NoError(t, json.Unmarshal(defaultConfigJSON, &defaultConfig))
	placeholders, found, err := unstructured.NestedStringMap(defaultConfig, configPath...)
	require.NoError(t, err)
	require.True(t, found, "the default config has no PodSecurity defaults")

	for _, tc := range []struct {
		name                string
		featureGateAccessor featuregates.FeatureGateAccess
	}{
		{
			name:                "enabled",
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{features.FeatureGateOpenShiftPodSecurityAdmission}, []configv1.FeatureGateName{}),
		},
		{
			name:                "disabled",
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{features.FeatureGateOpenShiftPodSecurityAdmission}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("PodSecurityTest", clock.RealClock{})
			observedConfig, errs := observePodSecurityAdmissionEnforcement(tc.featureGateAccessor, testRecorder, map[string]any{})
			require.Empty(t, errs)
			observed, found, err := unstructured.NestedStringMap(observedConfig, configPath...)
			require.NoError(t, err)
			require.True(t, found, "the observer sets no PodSecurity defaults")

			for key := range placeholders {
				require.Contains(t, observed, key, "the observer leaves a placeholder of the default config in place")
			}
			for _, mode := range []string{"enforce", "audit", "warn"} {
				_, err := psapi.ParseLevel(observed[mode])
				require.NoError(t, err, "%s: %q", mode, observed[mode])
				_, err = psapi.ParseVersion(observed[mode+"-version"])
				require.NoError(t, err, "%s-version: %q", mode, observed[mode+"-version"])
			}
		})
	}
}
