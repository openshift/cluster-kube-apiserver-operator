package certrotationtimeupgradeablecontroller

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestNewUpgradeableCondition(t *testing.T) {
	tests := []struct {
		name string

		input    map[string]string
		expected operatorv1.OperatorCondition
	}{
		{
			name:  "default",
			input: map[string]string{},
			expected: operatorv1.OperatorCondition{
				Type:   "CertRotationTimeUpgradeable",
				Status: "True",
				Reason: "DefaultCertRotationBase",
			},
		},
		{
			name:  "unknown",
			input: map[string]string{"other": ""},
			expected: operatorv1.OperatorCondition{
				Type:   "CertRotationTimeUpgradeable",
				Status: "True",
				Reason: "DefaultCertRotationBase",
			},
		},
		{
			name:  "changed",
			input: map[string]string{"base": "2y"},
			expected: operatorv1.OperatorCondition{
				Type:    "CertRotationTimeUpgradeable",
				Status:  "False",
				Reason:  "CertRotationBaseOverridden",
				Message: "configmap[\"\"]/ .data[\"base\"]==\"2y\"",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := newUpgradeableCondition(&corev1.ConfigMap{
				Data: test.input,
			})

			if !reflect.DeepEqual(test.expected, actual) {
				t.Fatal(spew.Sdump(actual))
			}
		})
	}
}
