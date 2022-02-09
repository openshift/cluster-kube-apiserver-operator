package webhooksupportabilitycontroller

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
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
	clusterVersionLister    configv1listers.ClusterVersionLister
}

// NewWebhookSupportabilityController sets Degraded=True conditions when a webhook service either cannot
// be found, or a tls connection cannot be established.
func NewWebhookSupportabilityController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	apiExtensionsInformers apiextensionsinformers.SharedInformerFactory,
	configInformers configinformers.SharedInformerFactory,
	recorder events.Recorder,
) *webhookSupportabilityController {
	kubeInformersForAllNamespaces := kubeInformersForNamespaces.InformersFor("")
	c := &webhookSupportabilityController{
		operatorClient:          operatorClient,
		mutatingWebhookLister:   kubeInformersForAllNamespaces.Admissionregistration().V1().MutatingWebhookConfigurations().Lister(),
		validatingWebhookLister: kubeInformersForAllNamespaces.Admissionregistration().V1().ValidatingWebhookConfigurations().Lister(),
		serviceLister:           kubeInformersForAllNamespaces.Core().V1().Services().Lister(),
		crdLister:               apiExtensionsInformers.Apiextensions().V1().CustomResourceDefinitions().Lister(),
		clusterVersionLister:    configInformers.Config().V1().ClusterVersions().Lister(),
	}
	c.Controller = factory.New().
		WithInformers(
			kubeInformersForAllNamespaces.Admissionregistration().V1().MutatingWebhookConfigurations().Informer(),
			kubeInformersForAllNamespaces.Admissionregistration().V1().ValidatingWebhookConfigurations().Informer(),
			kubeInformersForAllNamespaces.Core().V1().Services().Informer(),
			apiExtensionsInformers.Apiextensions().V1().CustomResourceDefinitions().Informer(),
			configInformers.Config().V1().ClusterVersions().Informer(),
		).
		WithSync(c.sync).
		ToController("webhookSupportabilityController", recorder)
	return c
}

func (c *webhookSupportabilityController) sync(ctx context.Context, controllerContext factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if !management.IsOperatorManaged(operatorSpec.ManagementState) {
		return nil
	}

	// do nothing while an upgrade is in progress
	clusterVersion, err := c.clusterVersionLister.Get("version")
	if err != nil {
		return err
	}
	desired := clusterVersion.Status.Desired.Version
	history := clusterVersion.Status.History
	// upgrade is in progress if there is no history, or the latest history entry matches the desired version and is not completed
	if len(history) == 0 || history[0].Version != desired || history[0].State != configv1.CompletedUpdate {
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
