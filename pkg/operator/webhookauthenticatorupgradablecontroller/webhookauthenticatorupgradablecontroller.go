package webhookauthenticatorupgradablecontroller

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// WebhooAuthenticatorUpgradeableController is a controller that sets upgradeable=false if authentication.spec.webhooktokenauthenticator is not nil
type WebhookAuthenticatorUpgradeableController struct {
	operatorClient        v1helpers.OperatorClient
	authenticationsLister configlistersv1.AuthenticationLister
}

func NewWebhookAuthenticatorUpgradeableController(
	operatorClient v1helpers.OperatorClient,
	configInformer configinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &WebhookAuthenticatorUpgradeableController{
		operatorClient:        operatorClient,
		authenticationsLister: configInformer.Config().V1().Authentications().Lister(),
	}

	return factory.New().WithInformers(
		operatorClient.Informer(),
		configInformer.Config().V1().Authentications().Informer(),
	).WithSync(c.sync).ToController("WebhookAuthenticatorUpgradeableController", eventRecorder.WithComponentSuffix("webhookauthenticator-upgradeable"))
}

func (c *WebhookAuthenticatorUpgradeableController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	authConfig, err := c.authenticationsLister.Get("cluster")
	if err != nil {
		return err
	}

	cond := newUpgradeableCondition(authConfig)
	if _, _, updateError := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(cond)); updateError != nil {
		return updateError
	}

	return nil
}

func newUpgradeableCondition(authConfig *configv1.Authentication) operatorv1.OperatorCondition {
	if authConfig.Spec.WebhookTokenAuthenticator == nil {
		return operatorv1.OperatorCondition{
			Type:   "AuthenticationConfigUpgradeable",
			Reason: "NoWebhookTokenAuthenticatorConfigured",
			Status: operatorv1.ConditionTrue,
		}
	}

	return operatorv1.OperatorCondition{
		Type:    "AuthenticationConfigUpgradeable",
		Status:  operatorv1.ConditionFalse,
		Reason:  "WebhookTokenAuthenticatorConfigured",
		Message: "upgrades are not allowed when authentication.config/cluster .spec.WebhookTokenAuthenticator is set",
	}

}
