package operator

import (
	"fmt"
	"reflect"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/blang/semver"
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorconfiginformerv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions/kubeapiserver/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
	"github.com/openshift/library-go/pkg/operator/versioning"
)

type DeploymentController struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	kubeClient kubernetes.Interface

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewDeploymentController(
	operatorConfigInformer operatorconfiginformerv1alpha1.KubeApiserverOperatorConfigInformer,
	namespacedKubeInformers informers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
	kubeClient kubernetes.Interface,
) *DeploymentController {
	c := &DeploymentController{
		operatorConfigClient: operatorConfigClient,
		kubeClient:           kubeClient,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "DeploymentController"),
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().Services().Informer().AddEventHandler(c.eventHandler())

	return c
}

func (c DeploymentController) sync() error {
	operatorConfig, err := c.operatorConfigClient.KubeApiserverOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	operatorConfigOriginal := operatorConfig.DeepCopy()

	switch operatorConfig.Spec.ManagementState {
	case operatorv1alpha1.Unmanaged:
		return nil

	case operatorv1alpha1.Removed:
		// TODO probably just fail.  This one shouldn't be removed.
		return nil
	}

	var currentActualVersion *semver.Version

	if operatorConfig.Status.CurrentAvailability != nil {
		ver, err := semver.Parse(operatorConfig.Status.CurrentAvailability.Version)
		if err != nil {
			utilruntime.HandleError(err)
		} else {
			currentActualVersion = &ver
		}
	}
	desiredVersion, err := semver.Parse(operatorConfig.Spec.Version)
	if err != nil {
		// TODO report failing status, we may actually attempt to do this in the "normal" error handling
		return err
	}

	v311_00_to_unknown := versioning.NewRangeOrDie("3.11.0", "3.12.0")

	switch {
	case v311_00_to_unknown.BetweenOrEmpty(currentActualVersion) && v311_00_to_unknown.Between(&desiredVersion):
		requeue, syncErr := createDeploymentController_v311_00_to_latest(c, operatorConfig)
		if requeue && syncErr == nil {
			return fmt.Errorf("synthetic requeue request")
		}
		err = syncErr

	default:
		operatorConfig.Status.TaskSummary = "unrecognized"
		if _, err := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig); err != nil {
			utilruntime.HandleError(err)
		}

		return fmt.Errorf("unrecognized state")
	}

	if err != nil {
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			v1alpha1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1alpha1.OperatorCondition{
				Type:    operatorv1alpha1.OperatorStatusTypeFailing,
				Status:  operatorv1alpha1.ConditionTrue,
				Reason:  "StatusUpdateError",
				Message: err.Error(),
			})
			if _, updateError := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig); updateError != nil {
				glog.Error(updateError)
			}
		}
		return err
	}

	return nil
}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *DeploymentController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting DeploymentController")
	defer glog.Infof("Shutting down DeploymentController")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *DeploymentController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *DeploymentController) processNextWorkItem() bool {
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
func (c *DeploymentController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
