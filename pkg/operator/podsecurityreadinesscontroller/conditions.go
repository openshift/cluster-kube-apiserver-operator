package podsecurityreadinesscontroller

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	// Historically, we assume that this is a customer issue, but
	// actually it means we don't know what the root cause is.
	PodSecurityUnknownType        = "PodSecurityCustomerEvaluationConditionsDetected"
	PodSecurityOpenshiftType      = "PodSecurityOpenshiftEvaluationConditionsDetected"
	PodSecurityRunLevelZeroType   = "PodSecurityRunLevelZeroEvaluationConditionsDetected"
	PodSecurityDisabledSyncerType = "PodSecurityDisabledSyncerEvaluationConditionsDetected"
	PodSecurityInconclusiveType   = "PodSecurityInconclusiveEvaluationConditionsDetected"
	PodSecurityUserSCCType        = "PodSecurityUserSCCEvaluationConditionsDetected"

	labelSyncControlLabel = "security.openshift.io/scc.podSecurityLabelSync"

	violationReason    = "PSViolationsDetected"
	inconclusiveReason = "PSViolationDecisionInconclusive"
)

type podSecurityOperatorConditions struct {
	violatingOpenShiftNamespaces      []string
	violatingRunLevelZeroNamespaces   []string
	violatingDisabledSyncerNamespaces []string
	violatingUserSCCNamespaces        []string
	violatingUnclassifiedNamespaces   []string
	inconclusiveNamespaces            []string
}

func (c *podSecurityOperatorConditions) addInconclusive(ns *corev1.Namespace) {
	c.inconclusiveNamespaces = append(c.inconclusiveNamespaces, ns.Name)
}

func (c *podSecurityOperatorConditions) addViolatingRunLevelZero(ns *corev1.Namespace) {
	c.violatingRunLevelZeroNamespaces = append(c.violatingRunLevelZeroNamespaces, ns.Name)
}

func (c *podSecurityOperatorConditions) addViolatingOpenShift(ns *corev1.Namespace) {
	c.violatingOpenShiftNamespaces = append(c.violatingOpenShiftNamespaces, ns.Name)
}

func (c *podSecurityOperatorConditions) addViolatingDisabledSyncer(ns *corev1.Namespace) {
	c.violatingDisabledSyncerNamespaces = append(c.violatingDisabledSyncerNamespaces, ns.Name)
}

func (c *podSecurityOperatorConditions) addUnclassifiedIssue(ns *corev1.Namespace) {
	c.violatingUnclassifiedNamespaces = append(c.violatingUnclassifiedNamespaces, ns.Name)
}

func (c *podSecurityOperatorConditions) addViolatingUserSCC(ns *corev1.Namespace) {
	c.violatingUserSCCNamespaces = append(c.violatingUserSCCNamespaces, ns.Name)
}

func makeCondition(conditionType, conditionReason string, namespaces []string) operatorv1.OperatorCondition {
	var messageFormatter string

	switch conditionReason {
	case violationReason:
		messageFormatter = "Violations detected in namespaces: %v"
	case inconclusiveReason:
		messageFormatter = "Could not evaluate violations for namespaces: %v"
	default:
		messageFormatter = "Unexpected condition for namespace: %v"
	}

	if len(namespaces) > 0 {
		sort.Strings(namespaces)
		return operatorv1.OperatorCondition{
			Type:               conditionType,
			Status:             operatorv1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             conditionReason,
			Message: fmt.Sprintf(
				messageFormatter,
				namespaces,
			),
		}
	}

	return operatorv1.OperatorCondition{
		Type:               conditionType,
		Status:             operatorv1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             "ExpectedReason",
	}
}

func (c *podSecurityOperatorConditions) toConditionFuncs() []v1helpers.UpdateStatusFunc {
	return []v1helpers.UpdateStatusFunc{
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityUnknownType, violationReason, c.violatingUnclassifiedNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityOpenshiftType, violationReason, c.violatingOpenShiftNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityRunLevelZeroType, violationReason, c.violatingRunLevelZeroNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityDisabledSyncerType, violationReason, c.violatingDisabledSyncerNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityUserSCCType, violationReason, c.violatingUserSCCNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityInconclusiveType, inconclusiveReason, c.inconclusiveNamespaces)),
	}
}
