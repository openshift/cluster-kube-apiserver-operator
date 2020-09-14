package certrotationtimeupgradeablecontroller

import (
	"context"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	coreinformersv1 "k8s.io/client-go/informers/core/v1"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
)

var (
	certRotationTimeUpgradeableControllerWorkQueueKey = "key"
)

// CertRotationTimeUpgradeableController is a controller that sets upgradeable=false if the cert rotation time has been adjusted.
type CertRotationTimeUpgradeableController struct {
	operatorClient  v1helpers.OperatorClient
	configMapLister corelistersv1.ConfigMapLister
}

func NewCertRotationTimeUpgradeableController(
	operatorClient v1helpers.OperatorClient,
	configMapInformer coreinformersv1.ConfigMapInformer,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &CertRotationTimeUpgradeableController{
		operatorClient:  operatorClient,
		configMapLister: configMapInformer.Lister(),
	}

	return factory.New().WithInformers(
		operatorClient.Informer(),
		configMapInformer.Informer(),
	).WithSync(c.sync).ToController("CertRotationTimeUpgradeableController", eventRecorder.WithComponentSuffix("certRotationTime-upgradeable"))
}

func (c *CertRotationTimeUpgradeableController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	certRotationTimeConfigMap, err := c.configMapLister.ConfigMaps("openshift-config").Get("unsupported-cert-rotation-config")
	if !errors.IsNotFound(err) && err != nil {
		return err
	}

	cond := newUpgradeableCondition(certRotationTimeConfigMap)
	if _, _, updateError := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(cond)); updateError != nil {
		return updateError
	}

	return nil
}

func newUpgradeableCondition(certRotationTimeConfigMap *corev1.ConfigMap) operatorv1.OperatorCondition {
	if certRotationTimeConfigMap == nil || len(certRotationTimeConfigMap.Data["base"]) == 0 {
		return operatorv1.OperatorCondition{
			Type:   "CertRotationTimeUpgradeable",
			Status: operatorv1.ConditionTrue,
			Reason: "DefaultCertRotationBase",
		}
	}

	return operatorv1.OperatorCondition{
		Type:    "CertRotationTimeUpgradeable",
		Status:  operatorv1.ConditionFalse,
		Reason:  "CertRotationBaseOverridden",
		Message: fmt.Sprintf("configmap[%q]/%s .data[\"base\"]==%q", certRotationTimeConfigMap.Namespace, certRotationTimeConfigMap.Name, certRotationTimeConfigMap.Data["base"]),
	}

}
