package featureupgradablecontroller

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestNewUpgradeableCondition(t *testing.T) {
	tests := []struct {
		name string

		features string
		expected operatorv1.OperatorCondition
	}{
		{
			name:     "default",
			features: "",
			expected: operatorv1.OperatorCondition{
				Reason: "AllowedFeatureGates_",
				Status: "True",
				Type:   "FeatureGatesUpgradeable",
			},
		},
		{
			name:     "unknown",
			features: "other",
			expected: operatorv1.OperatorCondition{
				Reason:  "RestrictedFeatureGates_other",
				Status:  "False",
				Type:    "FeatureGatesUpgradeable",
				Message: "\"other\" does not allow updates",
			},
		},
		{
			name:     "techpreview",
			features: string(configv1.TechPreviewNoUpgrade),
			expected: operatorv1.OperatorCondition{
				Reason:  "RestrictedFeatureGates_TechPreviewNoUpgrade",
				Status:  "False",
				Type:    "FeatureGatesUpgradeable",
				Message: "\"TechPreviewNoUpgrade\" does not allow updates",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := newUpgradeableCondition(&configv1.FeatureGate{
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.FeatureSet(test.features),
					},
				},
			})

			if !reflect.DeepEqual(test.expected, actual) {
				t.Fatal(spew.Sdump(actual))
			}
		})
	}
}
