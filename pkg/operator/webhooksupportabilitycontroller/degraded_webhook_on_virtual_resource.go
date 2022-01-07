package webhooksupportabilitycontroller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (c *webhookSupportabilityController) updateVirtualResourceAdmissionDegraded(ctx context.Context) v1helpers.UpdateStatusFunc {
	condition := operatorv1.OperatorCondition{
		Type:   VirtualResourceAdmissionDegradedType,
		Status: operatorv1.ConditionUnknown,
	}
	mutatingWebhookConfigurations, err := c.mutatingWebhookLister.List(labels.Everything())
	if err != nil {
		condition.Message = err.Error()
		return v1helpers.UpdateConditionFn(condition)
	}
	var msgs []string
	for _, config := range mutatingWebhookConfigurations {
		for _, webhook := range config.Webhooks {
			var matches []string
			for _, resource := range virtualResources {
				if rulesMatchResource(webhook.Rules, resource) {
					matches = append(matches, resource.GroupResource().String()+"/"+resource.Version)
				}
			}
			switch len(matches) {
			case 0:
			case 1:
				msgs = append(msgs, fmt.Sprintf("Mutating webhook %s matches a virtual resource %s.", webhook.Name, matches[0]))
			default:
				sort.Strings(matches)
				msgs = append(msgs, fmt.Sprintf("Mutating webhook %s matches multiple virtual resources: %s.", webhook.Name, strings.Join(matches, ", ")))
			}
		}
	}
	validatingWebhookConfigurations, err := c.validatingWebhookLister.List(labels.Everything())
	if err != nil {
		condition.Message = err.Error()
		return v1helpers.UpdateConditionFn(condition)
	}
	for _, config := range validatingWebhookConfigurations {
		for _, webhook := range config.Webhooks {
			var matches []string
			for _, resource := range virtualResources {
				if rulesMatchResource(webhook.Rules, resource) {
					matches = append(matches, resource.GroupResource().String()+"/"+resource.Version)
				}
			}
			switch len(matches) {
			case 0:
			case 1:
				msgs = append(msgs, fmt.Sprintf("Validating webhook %s matches a virtual resource %s", webhook.Name, matches[0]))
			default:
				sort.Strings(matches)
				msgs = append(msgs, fmt.Sprintf("Validating webhook %s matches multiple virtual resources: %s.", webhook.Name, strings.Join(matches, ", ")))
			}
		}
	}

	if len(msgs) > 0 {
		sort.Strings(msgs)
		condition.Message = strings.Join(msgs, "\n")
		condition.Reason = AdmissionWebhookMatchesVirtualResourceReason
		condition.Status = operatorv1.ConditionTrue
	} else {
		condition.Status = operatorv1.ConditionFalse
	}

	return v1helpers.UpdateConditionFn(condition)
}

var virtualResources = []schema.GroupVersionResource{
	{Group: "", Version: "v1", Resource: "bindings"},
	{Group: "authorization.k8s.io", Version: "v1", Resource: "localsubjectaccessreviews"},
	{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"},
	{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectrulesreviews"},
	{Group: "authorization.k8s.io", Version: "v1", Resource: "subjectaccessreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "localresourceaccessreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "localsubjectaccessreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "resourceaccessreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "selfsubjectrulesreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "subjectaccessreviews"},
	{Group: "authorization.openshift.io", Version: "v1", Resource: "subjectrulesreviews"},
}

func rulesMatchResource(rules []admissionregistrationv1.RuleWithOperations, resource schema.GroupVersionResource) bool {
	for _, rule := range rules {
		if ruleMatchesResource(rule, resource) {
			return true
		}
	}
	return false
}

func ruleMatchesResource(rule admissionregistrationv1.RuleWithOperations, gvr schema.GroupVersionResource) bool {
	var scope, operation, group, version, resource bool
	// scope - any
	scope = true
	// operation - any
	operation = true
	// group
	for _, g := range rule.APIGroups {
		if g == "*" || g == gvr.Group {
			group = true
			break
		}
	}
	// version
	for _, v := range rule.APIVersions {
		if v == "*" || v == gvr.Version {
			version = true
			break
		}
	}
	// resource
	for _, rr := range rule.Resources {
		// ignore sub-resource, match only resource
		segments := strings.SplitN(rr, "/", 2)
		r := segments[0]
		if r == "*" || r == gvr.Resource {
			resource = true
		}
	}
	return scope && operation && group && version && resource
}
