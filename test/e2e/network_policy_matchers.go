package e2e

import (
	"fmt"
	"strings"

	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// BeDefaultDenyPolicy succeeds when the NetworkPolicy has an empty podSelector,
// both Ingress and Egress policy types, and no allow rules.
func BeDefaultDenyPolicy() types.GomegaMatcher {
	return &defaultDenyMatcher{}
}

type defaultDenyMatcher struct {
	reason string
}

func (m *defaultDenyMatcher) Match(actual interface{}) (bool, error) {
	policy, ok := actual.(*networkingv1.NetworkPolicy)
	if !ok {
		return false, fmt.Errorf("expected *NetworkPolicy, got %T", actual)
	}
	if len(policy.Spec.PodSelector.MatchLabels) != 0 || len(policy.Spec.PodSelector.MatchExpressions) != 0 {
		m.reason = fmt.Sprintf("podSelector is not empty: %v", policy.Spec.PodSelector)
		return false, nil
	}
	policyTypes := sets.New[string]()
	for _, pt := range policy.Spec.PolicyTypes {
		policyTypes.Insert(string(pt))
	}
	if !policyTypes.Has("Ingress") || !policyTypes.Has("Egress") {
		m.reason = fmt.Sprintf("expected both Ingress and Egress policyTypes, got %v", policy.Spec.PolicyTypes)
		return false, nil
	}
	if len(policy.Spec.Ingress) != 0 {
		m.reason = fmt.Sprintf("expected no ingress rules, got %d", len(policy.Spec.Ingress))
		return false, nil
	}
	if len(policy.Spec.Egress) != 0 {
		m.reason = fmt.Sprintf("expected no egress rules, got %d", len(policy.Spec.Egress))
		return false, nil
	}
	return true, nil
}

func (m *defaultDenyMatcher) FailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s to be a default-deny-all policy: %s", p.Namespace, p.Name, m.reason)
}

func (m *defaultDenyMatcher) NegatedFailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s not to be a default-deny-all policy", p.Namespace, p.Name)
}

// SelectPods succeeds when the NetworkPolicy's podSelector has the given label.
func SelectPods(key, value string) types.GomegaMatcher {
	return &podSelectorLabelMatcher{key: key, value: value}
}

type podSelectorLabelMatcher struct {
	key, value string
}

func (m *podSelectorLabelMatcher) Match(actual interface{}) (bool, error) {
	policy, ok := actual.(*networkingv1.NetworkPolicy)
	if !ok {
		return false, fmt.Errorf("expected *NetworkPolicy, got %T", actual)
	}
	v, ok := policy.Spec.PodSelector.MatchLabels[m.key]
	return ok && v == m.value, nil
}

func (m *podSelectorLabelMatcher) FailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s podSelector to have %s=%s, got %v",
		p.Namespace, p.Name, m.key, m.value, p.Spec.PodSelector.MatchLabels)
}

func (m *podSelectorLabelMatcher) NegatedFailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s podSelector not to have %s=%s",
		p.Namespace, p.Name, m.key, m.value)
}

// SelectPodsExpression succeeds when the NetworkPolicy's podSelector has a matchExpression
// with the given key and values using the In operator.
func SelectPodsExpression(key string, values []string) types.GomegaMatcher {
	return &podSelectorExprMatcher{key: key, values: values}
}

type podSelectorExprMatcher struct {
	key    string
	values []string
}

func (m *podSelectorExprMatcher) Match(actual interface{}) (bool, error) {
	policy, ok := actual.(*networkingv1.NetworkPolicy)
	if !ok {
		return false, fmt.Errorf("expected *NetworkPolicy, got %T", actual)
	}
	expected := sets.New[string](m.values...)
	for _, expr := range policy.Spec.PodSelector.MatchExpressions {
		if expr.Key == m.key && expr.Operator == "In" {
			if sets.New[string](expr.Values...).Equal(expected) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *podSelectorExprMatcher) FailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s podSelector to have expression %s In %v",
		p.Namespace, p.Name, m.key, m.values)
}

func (m *podSelectorExprMatcher) NegatedFailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s podSelector not to have expression %s In %v",
		p.Namespace, p.Name, m.key, m.values)
}

// AllowIngressOnPort succeeds when the policy allows ingress from any source on the given TCP port.
func AllowIngressOnPort(port int32) types.GomegaMatcher {
	return &ingressPortMatcher{port: port}
}

type ingressPortMatcher struct {
	port int32
}

func (m *ingressPortMatcher) Match(actual interface{}) (bool, error) {
	policy, ok := actual.(*networkingv1.NetworkPolicy)
	if !ok {
		return false, fmt.Errorf("expected *NetworkPolicy, got %T", actual)
	}
	for _, rule := range policy.Spec.Ingress {
		if len(rule.From) != 0 {
			continue
		}
		for _, p := range rule.Ports {
			if p.Port != nil && p.Port.IntValue() == int(m.port) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *ingressPortMatcher) FailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s to allow ingress from any source on port %d",
		p.Namespace, p.Name, m.port)
}

func (m *ingressPortMatcher) NegatedFailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s not to allow ingress from any source on port %d",
		p.Namespace, p.Name, m.port)
}

// AllowAllTCPEgress succeeds when the policy has an unrestricted TCP egress rule
// (empty To field with a TCP port or no port restriction).
func AllowAllTCPEgress() types.GomegaMatcher {
	return &egressAllowAllTCPMatcher{}
}

type egressAllowAllTCPMatcher struct{}

func (m *egressAllowAllTCPMatcher) Match(actual interface{}) (bool, error) {
	policy, ok := actual.(*networkingv1.NetworkPolicy)
	if !ok {
		return false, fmt.Errorf("expected *NetworkPolicy, got %T", actual)
	}
	for _, rule := range policy.Spec.Egress {
		if len(rule.To) != 0 {
			continue
		}
		if len(rule.Ports) == 0 {
			return true, nil
		}
		for _, p := range rule.Ports {
			if p.Protocol == nil || *p.Protocol == corev1.ProtocolTCP {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *egressAllowAllTCPMatcher) FailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	var rules []string
	for i, rule := range p.Spec.Egress {
		rules = append(rules, fmt.Sprintf("rule[%d]: to=%d ports=%d", i, len(rule.To), len(rule.Ports)))
	}
	return fmt.Sprintf("expected %s/%s to have an allow-all TCP egress rule, got: %s",
		p.Namespace, p.Name, strings.Join(rules, "; "))
}

func (m *egressAllowAllTCPMatcher) NegatedFailureMessage(actual interface{}) string {
	p := actual.(*networkingv1.NetworkPolicy)
	return fmt.Sprintf("expected %s/%s not to have an allow-all TCP egress rule",
		p.Namespace, p.Name)
}
