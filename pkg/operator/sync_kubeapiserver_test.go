package operator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestImagePullPolicy(t *testing.T) {
	tests := []struct {
		operatorPolicy string
		expected       corev1.PullPolicy
		expectedError  bool
	}{
		{
			operatorPolicy: "Always",
			expected:       corev1.PullAlways,
		},
		{
			operatorPolicy: "",
			expectedError:  true,
		},
		{
			operatorPolicy: "Unknown",
			expectedError:  true,
		},
	}

	for _, tc := range tests {
		switch corev1.PullPolicy(tc.operatorPolicy) {
		case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
			if tc.expected != corev1.PullPolicy(tc.operatorPolicy) {
				t.Errorf("test case %+v got unexpected policy: %v", tc, corev1.PullPolicy(tc.operatorPolicy))
			}
		case "":
			if tc.expected != "" {
				t.Errorf("test case %+v unexpected empty policy, expected %v", tc, tc.expected)
			}
		default:
			if !tc.expectedError {
				t.Errorf("test case %+v unexpected error, expected %v", tc, tc.expected)
			}
		}
	}
}
