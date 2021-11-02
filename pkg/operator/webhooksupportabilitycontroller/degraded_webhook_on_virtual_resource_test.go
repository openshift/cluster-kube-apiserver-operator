package webhooksupportabilitycontroller

import (
	"context"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	admissionregistrationv1listers "k8s.io/client-go/listers/admissionregistration/v1"
	"k8s.io/client-go/tools/cache"
)

func TestUpdateVirtualResourceAdmissionDegraded(t *testing.T) {

	testCases := []struct {
		name                     string
		mutatingWebhookConfigs   []*admissionregistrationv1.MutatingWebhookConfiguration
		validatingWebhookConfigs []*admissionregistrationv1.ValidatingWebhookConfiguration
		expected                 operatorv1.OperatorCondition
	}{
		{
			name: "NoHooks",
			expected: operatorv1.OperatorCondition{
				Type:   VirtualResourceAdmissionDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "MatchWildCards",
			mutatingWebhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingRule(all, all, all)),
				),
			},
			validatingWebhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("vwc10",
					withValidatingWebhook("vw10", withValidatingRule(all, all, all)),
				),
			},
			expected: operatorv1.OperatorCondition{
				Type:   VirtualResourceAdmissionDegradedType,
				Status: operatorv1.ConditionTrue,
				Reason: AdmissionWebhookMatchesVirtualResourceReason,
				Message: "Mutating webhook mw10 matches multiple virtual resources: " +
					"bindings/v1, " +
					"localresourceaccessreviews.authorization.openshift.io/v1, " +
					"localsubjectaccessreviews.authorization.k8s.io/v1, " +
					"localsubjectaccessreviews.authorization.openshift.io/v1, " +
					"resourceaccessreviews.authorization.openshift.io/v1, " +
					"selfsubjectaccessreviews.authorization.k8s.io/v1, " +
					"selfsubjectrulesreviews.authorization.k8s.io/v1, " +
					"selfsubjectrulesreviews.authorization.openshift.io/v1, " +
					"subjectaccessreviews.authorization.k8s.io/v1, " +
					"subjectaccessreviews.authorization.openshift.io/v1, " +
					"subjectrulesreviews.authorization.openshift.io/v1.\n" +
					"Validating webhook vw10 matches multiple virtual resources: " +
					"bindings/v1, " +
					"localresourceaccessreviews.authorization.openshift.io/v1, " +
					"localsubjectaccessreviews.authorization.k8s.io/v1, " +
					"localsubjectaccessreviews.authorization.openshift.io/v1, " +
					"resourceaccessreviews.authorization.openshift.io/v1, " +
					"selfsubjectaccessreviews.authorization.k8s.io/v1, " +
					"selfsubjectrulesreviews.authorization.k8s.io/v1, " +
					"selfsubjectrulesreviews.authorization.openshift.io/v1, " +
					"subjectaccessreviews.authorization.k8s.io/v1, " +
					"subjectaccessreviews.authorization.openshift.io/v1, " +
					"subjectrulesreviews.authorization.openshift.io/v1.",
			},
		},
		{
			name: "MatchExact",
			mutatingWebhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingRule("authorization.openshift.io", "v1", "subjectaccessreviews")),
				),
			},
			validatingWebhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("vwc10",
					withValidatingWebhook("vw10", withValidatingRule("authorization.k8s.io", "v1", "subjectaccessreviews")),
				),
			},
			expected: operatorv1.OperatorCondition{
				Type:   VirtualResourceAdmissionDegradedType,
				Status: operatorv1.ConditionTrue,
				Reason: AdmissionWebhookMatchesVirtualResourceReason,
				Message: "Mutating webhook mw10 matches a virtual resource subjectaccessreviews.authorization.openshift.io/v1.\n" +
					"Validating webhook vw10 matches a virtual resource subjectaccessreviews.authorization.k8s.io/v1",
			},
		},
		{
			name: "MatchSome",
			mutatingWebhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingRule(all, all, "subjectaccessreviews")),
				),
			},
			expected: operatorv1.OperatorCondition{
				Type:   VirtualResourceAdmissionDegradedType,
				Status: operatorv1.ConditionTrue,
				Reason: AdmissionWebhookMatchesVirtualResourceReason,
				Message: "Mutating webhook mw10 matches multiple virtual resources: " +
					"subjectaccessreviews.authorization.k8s.io/v1, " +
					"subjectaccessreviews.authorization.openshift.io/v1.",
			},
		},
	}
	// 	{Group: "authorization.openshift.io", Version: "v1", Resource: "subjectaccessreviews"},
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := webhookSupportabilityController{}

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.validatingWebhookConfigs {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.validatingWebhookLister = admissionregistrationv1listers.NewValidatingWebhookConfigurationLister(indexer)

			indexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.mutatingWebhookConfigs {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.mutatingWebhookLister = admissionregistrationv1listers.NewMutatingWebhookConfigurationLister(indexer)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			result := c.updateVirtualResourceAdmissionDegraded(ctx)
			status := &operatorv1.OperatorStatus{}
			err := result(status)
			if err != nil {
				t.Fatal(err)
			}
			if len(status.Conditions) != 1 {
				t.Log(status)
				t.Fatal("expected exactly one condition")
			}
			requireCondition(t, tc.expected, status.Conditions[0])
		})
	}

}
