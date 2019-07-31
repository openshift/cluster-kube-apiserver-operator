package certrotationtimeupgradeablecontroller

import (
	"fmt"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformersv1 "k8s.io/client-go/informers/core/v1"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

var (
	certRotationTimeUpgradeableControllerWorkQueueKey = "key"
)

// CertRotationTimeUpgradeableController is a controller that sets upgradeable=false if the cert rotation time has been adjusted.
type CertRotationTimeUpgradeableController struct {
	operatorClient  v1helpers.OperatorClient
	configMapLister corelistersv1.ConfigMapLister

	cachesToSync  []cache.InformerSynced
	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder
}

func NewCertRotationTimeUpgradeableController(
	operatorClient v1helpers.OperatorClient,
	configMapInformer coreinformersv1.ConfigMapInformer,
	eventRecorder events.Recorder,
) *CertRotationTimeUpgradeableController {
	c := &CertRotationTimeUpgradeableController{

		operatorClient:  operatorClient,
		configMapLister: configMapInformer.Lister(),
		eventRecorder:   eventRecorder.WithComponentSuffix("certRotationTime-upgradeable"),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "CertRotationTimeUpgradeableController"),
	}

	operatorClient.Informer().AddEventHandler(c.eventHandler())
	configMapInformer.Informer().AddEventHandler(c.eventHandler())

	c.cachesToSync = append(c.cachesToSync, operatorClient.Informer().HasSynced, configMapInformer.Informer().HasSynced)
	return c
}

func (c *CertRotationTimeUpgradeableController) sync() error {
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

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *CertRotationTimeUpgradeableController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting CertRotationTimeUpgradeableController")
	defer klog.Infof("Shutting down CertRotationTimeUpgradeableController")
	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *CertRotationTimeUpgradeableController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *CertRotationTimeUpgradeableController) processNextWorkItem() bool {
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
func (c *CertRotationTimeUpgradeableController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(certRotationTimeUpgradeableControllerWorkQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(certRotationTimeUpgradeableControllerWorkQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(certRotationTimeUpgradeableControllerWorkQueueKey) },
	}
}
