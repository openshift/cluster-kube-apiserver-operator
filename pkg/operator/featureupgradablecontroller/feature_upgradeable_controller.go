package featureupgradablecontroller

import (
	"fmt"
	"time"

	"k8s.io/klog"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var (
	featureUpgradeableControllerWorkQueueKey = "key"

	featureGatesAllowingUpgrade = sets.NewString("", string(configv1.LatencySensitive))
)

// FeatureUpgradeableController is a controller that sets upgradeable=false if anything outside the whitelist is the specified featuregates.
type FeatureUpgradeableController struct {
	operatorClient    v1helpers.OperatorClient
	featureGateLister configlistersv1.FeatureGateLister

	cachesToSync  []cache.InformerSynced
	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder
}

func NewFeatureUpgradeableController(
	operatorClient v1helpers.OperatorClient,
	configInformer configinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
) *FeatureUpgradeableController {
	c := &FeatureUpgradeableController{

		operatorClient:    operatorClient,
		featureGateLister: configInformer.Config().V1().FeatureGates().Lister(),
		eventRecorder:     eventRecorder.WithComponentSuffix("feature-upgradeable"),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "FeatureUpgradeableController"),
	}

	operatorClient.Informer().AddEventHandler(c.eventHandler())
	configInformer.Config().V1().FeatureGates().Informer().AddEventHandler(c.eventHandler())

	c.cachesToSync = append(c.cachesToSync, operatorClient.Informer().HasSynced, configInformer.Config().V1().FeatureGates().Informer().HasSynced)
	return c
}

func (c *FeatureUpgradeableController) sync() error {
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

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *FeatureUpgradeableController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting FeatureUpgradeableController")
	defer klog.Infof("Shutting down FeatureUpgradeableController")
	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *FeatureUpgradeableController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *FeatureUpgradeableController) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *FeatureUpgradeableController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(featureUpgradeableControllerWorkQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(featureUpgradeableControllerWorkQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(featureUpgradeableControllerWorkQueueKey) },
	}
}
