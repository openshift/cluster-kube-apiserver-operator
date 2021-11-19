package webhooksupportabilitycontroller

import (
	"context"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (c *webhookSupportabilityController) updateCRDConversionWebhookConfigurationDegraded(ctx context.Context) v1helpers.UpdateStatusFunc {
	condition := operatorv1.OperatorCondition{
		Type:   CRDConversionWebhookConfigurationDegradedType,
		Status: operatorv1.ConditionUnknown,
	}
	crds, err := c.crdLister.List(labels.Everything())
	if err != nil {
		condition.Message = err.Error()
		return v1helpers.UpdateConditionFn(condition)
	}
	var webhookInfos []webhookInfo
	for _, crd := range crds {
		conversion := crd.Spec.Conversion
		if conversion == nil || conversion.Strategy != v1.WebhookConverter {
			continue
		}
		clientConfig := conversion.Webhook.ClientConfig
		if clientConfig == nil || clientConfig.Service == nil {
			continue
		}
		info := webhookInfo{
			Name:     crd.Name,
			CABundle: clientConfig.CABundle,
			Service: &serviceReference{
				Namespace: clientConfig.Service.Namespace,
				Name:      clientConfig.Service.Name,
				Port:      clientConfig.Service.Port,
			},
		}
		webhookInfos = append(webhookInfos, info)
	}
	return c.updateWebhookConfigurationDegraded(ctx, condition, webhookInfos)
}
