package featureupgradablecontroller

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	featureGatesAllowingUpgrade = sets.NewString("")
)

// FeatureUpgradeableController is a controller that sets upgradeable=false if anything outside the allowed list is the specified featuregates.
type FeatureUpgradeableController struct {
	operatorClient    v1helpers.OperatorClient
	featureGateLister configlistersv1.FeatureGateLister
}

func NewFeatureUpgradeableController(
	operatorClient v1helpers.OperatorClient,
	configInformer configinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &FeatureUpgradeableController{
		operatorClient:    operatorClient,
		featureGateLister: configInformer.Config().V1().FeatureGates().Lister(),
	}

	return factory.New().WithInformers(
		operatorClient.Informer(),
		configInformer.Config().V1().FeatureGates().Informer(),
	).WithSync(c.sync).ToController("FeatureUpgradeableController", eventRecorder.WithComponentSuffix("feature-upgradeable"))
}

func (c *FeatureUpgradeableController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	featureGates, err := c.featureGateLister.Get("cluster")
	if err != nil {
		return err
	}

	cond := newUpgradeableCondition(featureGates)
	if _, _, updateError := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(cond)); updateError != nil {
		return updateError
	}

	return nil
}

func newUpgradeableCondition(featureGates *configv1.FeatureGate) operatorv1.OperatorCondition {
	if featureGatesAllowingUpgrade.Has(string(featureGates.Spec.FeatureSet)) {
		return operatorv1.OperatorCondition{
			Type:   "FeatureGatesUpgradeable",
			Reason: "AllowedFeatureGates_" + string(featureGates.Spec.FeatureSet),
			Status: operatorv1.ConditionTrue,
		}
	}

	return operatorv1.OperatorCondition{
		Type:    "FeatureGatesUpgradeable",
		Status:  operatorv1.ConditionFalse,
		Reason:  "RestrictedFeatureGates_" + string(featureGates.Spec.FeatureSet),
		Message: fmt.Sprintf("%q does not allow updates", string(featureGates.Spec.FeatureSet)),
	}

}
