package operator

import (
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	condition "github.com/openshift/library-go/pkg/operator/condition"
)

func TestNewDegradedInertia(t *testing.T) {
	inertia := newDegradedInertia()

	tests := []struct {
		conditionType string
		expected      time.Duration
	}{
		{
			conditionType: condition.NodeControllerDegradedConditionType,
			expected:      10 * time.Minute,
		},
		{
			conditionType: condition.StaticPodsDegradedConditionType,
			expected:      10 * time.Minute,
		},
		{
			conditionType: "TargetConfigControllerDegraded",
			expected:      2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.conditionType, func(t *testing.T) {
			got := inertia(operatorv1.OperatorCondition{Type: tt.conditionType})
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
