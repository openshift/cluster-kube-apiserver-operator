package podsecurityreadinesscontroller

import (
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	PodSecurityCustomerType     = "PodSecurityCustomerEvaluationConditionsDetected"
	PodSecurityOpenshiftType    = "PodSecurityOpenshiftEvaluationConditionsDetected"
	PodSecurityRunLevelZeroType = "PodSecurityRunLevelZeroEvaluationConditionsDetected"
)

var (
	// run-level zero namespaces, shouldn't avoid openshift namespaces
	runLevelZeroNamespaces = sets.New[string](
		"default",
		"kube-system",
		"kube-public",
	)
)

type podSecurityOperatorConditions struct {
	violatingOpenShiftNamespaces    []string
	violatingRunLevelZeroNamespaces []string
	violatingCustomerNamespaces     []string
}

func (c *podSecurityOperatorConditions) addViolation(name string) {
	if runLevelZeroNamespaces.Has(name) {
		c.violatingRunLevelZeroNamespaces = append(c.violatingRunLevelZeroNamespaces, name)
		return
	}

	isOpenShift := strings.HasPrefix(name, "openshift")
	if isOpenShift {
		c.violatingOpenShiftNamespaces = append(c.violatingOpenShiftNamespaces, name)
		return
	}

	c.violatingCustomerNamespaces = append(c.violatingCustomerNamespaces, name)
}

func makeCondition(conditionType string, namespaces []string) operatorv1.OperatorCondition {
	if len(namespaces) > 0 {
		sort.Strings(namespaces)
		return operatorv1.OperatorCondition{
			Type:               conditionType,
			Status:             operatorv1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "PSViolationsDetected",
			Message: fmt.Sprintf(
				"Violations detected in namespaces: %v",
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
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityCustomerType, c.violatingCustomerNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityOpenshiftType, c.violatingOpenShiftNamespaces)),
		v1helpers.UpdateConditionFn(makeCondition(PodSecurityRunLevelZeroType, c.violatingRunLevelZeroNamespaces)),
	}
}
