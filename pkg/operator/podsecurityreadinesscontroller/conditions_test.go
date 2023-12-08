package podsecurityreadinesscontroller

import (
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestCondition(t *testing.T) {
	t.Run("with namespaces", func(t *testing.T) {
		namespaces := []string{"namespace1", "namespace2"}
		expectedCondition := operatorv1.OperatorCondition{
			Type:    PodSecurityCustomerType,
			Status:  operatorv1.ConditionTrue,
			Reason:  "PSViolationsDetected",
			Message: "Violations detected in namespaces: [namespace1 namespace2]",
		}

		condition := makeCondition(PodSecurityCustomerType, namespaces)

		if condition.Type != expectedCondition.Type {
			t.Errorf("expected condition type %s, got %s", expectedCondition.Type, condition.Type)
		}

		if condition.Status != expectedCondition.Status {
			t.Errorf("expected condition status %s, got %s", expectedCondition.Status, condition.Status)
		}

		if condition.Reason != expectedCondition.Reason {
			t.Errorf("expected condition reason %s, got %s", expectedCondition.Reason, condition.Reason)
		}

		if condition.Message != expectedCondition.Message {
			t.Errorf("expected condition message %s, got %s", expectedCondition.Message, condition.Message)
		}
	})

	t.Run("without namespaces", func(t *testing.T) {
		namespaces := []string{}
		expectedCondition := operatorv1.OperatorCondition{
			Type:   PodSecurityCustomerType,
			Status: operatorv1.ConditionFalse,
			Reason: "ExpectedReason",
		}

		condition := makeCondition(PodSecurityCustomerType, namespaces)

		if condition.Type != expectedCondition.Type {
			t.Errorf("expected condition type %s, got %s", expectedCondition.Type, condition.Type)
		}

		if condition.Status != expectedCondition.Status {
			t.Errorf("expected condition status %s, got %s", expectedCondition.Status, condition.Status)
		}

		if condition.Reason != expectedCondition.Reason {
			t.Errorf("expected condition reason %s, got %s", expectedCondition.Reason, condition.Reason)
		}

		if condition.Message != expectedCondition.Message {
			t.Errorf("expected condition message %s, got %s", expectedCondition.Message, condition.Message)
		}
	})

	t.Run("without anything", func(t *testing.T) {
		cond := podSecurityOperatorConditions{}
		cond.addViolation("hello world")
	})
}
