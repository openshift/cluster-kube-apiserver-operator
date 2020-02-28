package defaultscccontroller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestNewCondition(t *testing.T) {
	conditionType := "DefaultSecurityContextConstraintsUpgradeable"

	tests := []struct {
		name          string
		mutated       []string
		conditionWant operatorv1.OperatorCondition
	}{
		{
			name: "WithMutation",
			mutated: []string{
				"anyuid",
				"hostaccess",
			},
			conditionWant: operatorv1.OperatorCondition{
				Type:    conditionType,
				Reason:  "Mutated",
				Status:  operatorv1.ConditionFalse,
				Message: "Default SecurityContextConstraints object(s) have mutated [anyuid hostaccess]",
			},
		},
		{
			name:    "WithoutMutation",
			mutated: []string{},
			conditionWant: operatorv1.OperatorCondition{
				Type:    conditionType,
				Reason:  "AsExpected",
				Status:  operatorv1.ConditionTrue,
				Message: "",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conditionGot := NewCondition(test.mutated)
			assert.Equal(t, test.conditionWant, conditionGot)
		})
	}
}
