package auth

import (
	"encoding/json"
	"strings"
	"testing"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObservePodSecurityAdmissionEnforcement(t *testing.T) {
	privilegedMap := map[string]interface{}{}
	require.NoError(t, SetPodSecurityAdmissionToEnforcePrivileged(privilegedMap))
	privilegedJSON, err := json.Marshal(privilegedMap)
	require.NoError(t, err)

	restrictedMap := map[string]interface{}{}
	require.NoError(t, SetPodSecurityAdmissionToEnforceRestricted(restrictedMap))
	restrictedJSON, err := json.Marshal(restrictedMap)
	require.NoError(t, err)

	defaultFeatureSet := &configv1.FeatureGate{
		Spec: configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet:      "",
				CustomNoUpgrade: nil,
			},
		},
	}

	corruptFeatureSet := &configv1.FeatureGate{
		Spec: configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet:      "Bad",
				CustomNoUpgrade: nil,
			},
		},
	}

	disabledFeatureSet := &configv1.FeatureGate{
		Spec: configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet: "CustomNoUpgrade",
				CustomNoUpgrade: &configv1.CustomFeatureGates{
					Enabled:  nil,
					Disabled: []string{"OpenShiftPodSecurityAdmission"},
				},
			},
		},
	}

	for _, tc := range []struct {
		name         string
		existingJSON string
		featureGate  *configv1.FeatureGate
		expectedErr  string
		expectedJSON string
	}{
		{
			name:         "enforce",
			existingJSON: string(privilegedJSON),
			featureGate:  defaultFeatureSet,
			expectedErr:  "",
			expectedJSON: string(restrictedJSON),
		},
		{
			name:         "corrupt-1",
			existingJSON: string(privilegedJSON),
			featureGate:  corruptFeatureSet,
			expectedErr:  "not found",
			expectedJSON: string(privilegedJSON),
		},
		{
			name:         "corrupt-2",
			existingJSON: string(restrictedJSON),
			featureGate:  corruptFeatureSet,
			expectedErr:  "not found",
			expectedJSON: string(restrictedJSON),
		},
		{
			name:         "disabled",
			existingJSON: string(restrictedJSON),
			featureGate:  disabledFeatureSet,
			expectedErr:  "",
			expectedJSON: string(privilegedJSON),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest")
			existingConfig := map[string]interface{}{}
			require.NoError(t, json.Unmarshal([]byte(tc.existingJSON), &existingConfig))

			actual, errs := observePodSecurityAdmissionEnforcement(tc.featureGate, testRecorder, existingConfig)

			switch {
			case len(errs) == 0 && len(tc.expectedErr) == 0:
			case len(errs) == 0 && len(tc.expectedErr) > 0:
				t.Fatalf("missing err: %v", tc.expectedErr)

			case len(errs) > 0 && len(tc.expectedErr) == 0:
				t.Fatal(errs)
			case len(errs) > 0 && len(tc.expectedErr) > 0 && !strings.Contains(utilerrors.NewAggregate(errs).Error(), tc.expectedErr):
				t.Fatalf("missing err: %v in \n%v", tc.expectedErr, errs)
			}

			actualJSON, err := json.Marshal(actual)
			require.NoError(t, err)

			require.Equal(t, tc.expectedJSON, string(actualJSON), cmp.Diff(tc.expectedJSON, string(actualJSON)))
		})
	}
}
