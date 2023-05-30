package webhooksupportabilitycontroller

import (
	"context"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	apiextensionslistersv1 "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1"
	admissionregistrationlistersv1 "k8s.io/client-go/listers/admissionregistration/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type webhookSupportabilityController struct {
	factory.Controller
	operatorClient          v1helpers.StaticPodOperatorClient
	mutatingWebhookLister   admissionregistrationlistersv1.MutatingWebhookConfigurationLister
	validatingWebhookLister admissionregistrationlistersv1.ValidatingWebhookConfigurationLister
	serviceLister           corev1listers.ServiceLister
	crdLister               apiextensionslistersv1.CustomResourceDefinitionLister
}

// NewWebhookSupportabilityController sets Degraded=True conditions when a webhook service either cannot
// be found, or a tls connection cannot be established.
func NewWebhookSupportabilityController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	apiExtensionsInformers apiextensionsinformers.SharedInformerFactory,
	recorder events.Recorder,
) *webhookSupportabilityController {
	kubeInformersForAllNamespaces := kubeInformersForNamespaces.InformersFor("")
	c := &webhookSupportabilityController{
		operatorClient:          operatorClient,
		mutatingWebhookLister:   kubeInformersForAllNamespaces.Admissionregistration().V1().MutatingWebhookConfigurations().Lister(),
		validatingWebhookLister: kubeInformersForAllNamespaces.Admissionregistration().V1().ValidatingWebhookConfigurations().Lister(),
		serviceLister:           kubeInformersForAllNamespaces.Core().V1().Services().Lister(),
		crdLister:               apiExtensionsInformers.Apiextensions().V1().CustomResourceDefinitions().Lister(),
	}
	c.Controller = factory.New().
		WithInformers(
			kubeInformersForAllNamespaces.Admissionregistration().V1().MutatingWebhookConfigurations().Informer(),
			kubeInformersForAllNamespaces.Admissionregistration().V1().ValidatingWebhookConfigurations().Informer(),
			kubeInformersForAllNamespaces.Core().V1().Services().Informer(),
		).
		WithFilteredEventsInformers(func(obj interface{}) bool {
			if crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
				return hasCRDConversionWebhookConfiguration(crd)
			}
			return true // re-queue just in case, the checks are fairly cheap
		}, apiExtensionsInformers.Apiextensions().V1().CustomResourceDefinitions().Informer()).
		WithSync(c.sync).
		ToController("webhookSupportabilityController", recorder)
	return c
}

func (c *webhookSupportabilityController) sync(ctx context.Context, _ factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if !management.IsOperatorManaged(operatorSpec.ManagementState) {
		return nil
	}

	var updates []v1helpers.UpdateStatusFunc
	updates = append(updates, c.updateMutatingAdmissionWebhookConfigurationDegraded(ctx))
	updates = append(updates, c.updateValidatingAdmissionWebhookConfigurationDegradedStatus(ctx))
	updates = append(updates, c.updateCRDConversionWebhookConfigurationDegraded(ctx))
	updates = append(updates, c.updateVirtualResourceAdmissionDegraded(ctx))

	_, _, err = v1helpers.UpdateStatus(ctx, c.operatorClient, updates...)
	return err
}
