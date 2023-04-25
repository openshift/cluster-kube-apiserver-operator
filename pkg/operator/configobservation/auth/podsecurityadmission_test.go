package auth

import (
	"encoding/json"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
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

	defaultFeatureSet := featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{})

	// TODO provide a hardcoded test harness that allows not-ready and error
	//corruptFeatureSet := &configv1.FeatureGate{
	//	Spec: configv1.FeatureGateSpec{
	//		FeatureGateSelection: configv1.FeatureGateSelection{
	//			FeatureSet:      "Bad",
	//			CustomNoUpgrade: nil,
	//		},
	//	},
	//}

	disabledFeatureSet := featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{}, []configv1.FeatureGateName{"OpenShiftPodSecurityAdmission"})

	for _, tc := range []struct {
		name                string
		existingJSON        string
		featureGateAccessor featuregates.FeatureGateAccess
		expectedErr         string
		expectedJSON        string
	}{
		{
			name:                "enforce",
			existingJSON:        string(privilegedJSON),
			featureGateAccessor: defaultFeatureSet,
			expectedErr:         "",
			expectedJSON:        string(restrictedJSON),
		},
		//{
		//	name:                "corrupt-1",
		//	existingJSON:        string(privilegedJSON),
		//	featureGateAccessor: corruptFeatureSet,
		//	expectedErr:         "not found",
		//	expectedJSON:        string(privilegedJSON),
		//},
		//{
		//	name:                "corrupt-2",
		//	existingJSON:        string(restrictedJSON),
		//	featureGateAccessor: corruptFeatureSet,
		//	expectedErr:         "not found",
		//	expectedJSON:        string(restrictedJSON),
		//},
		{
			name:                "disabled",
			existingJSON:        string(restrictedJSON),
			featureGateAccessor: disabledFeatureSet,
			expectedErr:         "",
			expectedJSON:        string(privilegedJSON),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest")
			existingConfig := map[string]interface{}{}
			require.NoError(t, json.Unmarshal([]byte(tc.existingJSON), &existingConfig))

			actual, errs := observePodSecurityAdmissionEnforcement(tc.featureGateAccessor, testRecorder, existingConfig)

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
