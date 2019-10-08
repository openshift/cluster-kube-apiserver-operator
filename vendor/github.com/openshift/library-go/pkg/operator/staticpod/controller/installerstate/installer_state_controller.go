package installerstate

import (
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const installerStateControllerWorkQueueKey = "key"

// maxToleratedPodPendingDuration is the maximum time we tolerate installer pod in pending state
var maxToleratedPodPendingDuration = 5 * time.Minute

type InstallerStateController struct {
	podsGetter      corev1client.PodsGetter
	queue           workqueue.RateLimitingInterface
	cachesToSync    []cache.InformerSynced
	targetNamespace string
	operatorClient  v1helpers.StaticPodOperatorClient
	eventRecorder   events.Recorder

	timeNowFn func() time.Time
}

func NewInstallerStateController(kubeInformersForTargetNamespace informers.SharedInformerFactory,
	podsGetter corev1client.PodsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	targetNamespace string,
	recorder events.Recorder,
) *InstallerStateController {
	c := &InstallerStateController{
		podsGetter:      podsGetter,
		targetNamespace: targetNamespace,
		operatorClient:  operatorClient,
		eventRecorder:   recorder.WithComponentSuffix("installer-state-controller"),
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "InstallerStateController"),
		timeNowFn:       time.Now,
	}

	c.cachesToSync = append(c.cachesToSync, kubeInformersForTargetNamespace.Core().V1().Pods().Informer().HasSynced)
	kubeInformersForTargetNamespace.Core().V1().Pods().Informer().AddEventHandler(c.eventHandler())

	return c
}

func (c *InstallerStateController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(installerStateControllerWorkQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(installerStateControllerWorkQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(installerStateControllerWorkQueueKey) },
	}
}

// degradedConditionNames lists all supported condition types.
var degradedConditionNames = []string{
	"InstallerPodPendingDegraded",
	"InstallerPodContainerWaitingDegraded",
}

func (c *InstallerStateController) sync() error {
	pods, err := c.podsGetter.Pods(c.targetNamespace).List(metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"app": "installer"}).String(),
	})
	if err != nil {
		return err
	}

	// collect all pods that are in pending state for longer than maxToleratedPodPendingDuration
	pendingPods := []*v1.Pod{}
	hasPendingPods := false
	for _, pod := range pods.Items {
		if pod.Status.Phase != v1.PodPending {
			continue
		}
		// we need to track this so we can requeue faster when there are pending pods
		hasPendingPods = true

		if pod.Status.StartTime == nil {
			continue
		}
		pendingTime := c.timeNowFn().Sub(pod.Status.StartTime.Time)
		if pendingTime >= maxToleratedPodPendingDuration {
			pendingPods = append(pendingPods, pod.DeepCopy())
		}
	}

	// in theory, there should never be two installer pods pending as we don't roll new installer pod
	// until the previous/existing pod has finished its job.
	foundConditions := c.handlePendingInstallerPods(pendingPods)

	updateConditionFuncs := []v1helpers.UpdateStaticPodStatusFunc{}

	// check the supported degraded foundConditions and check if any pending pod matching them.
	for _, degradedConditionName := range degradedConditionNames {
		// clean up existing foundConditions
		updatedCondition := operatorv1.OperatorCondition{
			Type:   degradedConditionName,
			Status: operatorv1.ConditionFalse,
		}
		if condition := v1helpers.FindOperatorCondition(foundConditions, degradedConditionName); condition != nil {
			updatedCondition = *condition
		}
		updateConditionFuncs = append(updateConditionFuncs, v1helpers.UpdateStaticPodConditionFn(updatedCondition))
	}

	if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, updateConditionFuncs...); err != nil {
		return err
	}

	// if we found pending pods, requeue faster and do not wait for informer as the pod can be stuck in Pending
	// with no update for long time and we want to track that time.
	if hasPendingPods {
		c.queue.AddAfter(installerStateControllerWorkQueueKey, 5*time.Second)
	}

	return nil
}

func (c *InstallerStateController) handlePendingInstallerPods(pods []*v1.Pod) []operatorv1.OperatorCondition {
	conditions := []operatorv1.OperatorCondition{}
	for _, pod := range pods {
		// at this poind we already know the pod is pending for longer than expected
		pendingTime := c.timeNowFn().Sub(pod.Status.StartTime.Time)

		// the pod is in the pending state for longer than maxToleratedPodPendingDuration, report the reason and message
		// as degraded condition for the operator.
		if len(pod.Status.Reason) > 0 {
			condition := operatorv1.OperatorCondition{
				Type:    "InstallerPodPendingDegraded",
				Reason:  pod.Status.Reason,
				Status:  operatorv1.ConditionTrue,
				Message: fmt.Sprintf("Pod %q is Pending for %s because %s", pod.Name, pendingTime, pod.Status.Message),
			}
			conditions = append(conditions, condition)
			c.eventRecorder.Warningf(condition.Reason, condition.Message)
		}

		// one or more containers are in waiting state for longer than maxToleratedPodPendingDuration, report the reason and message
		// as degraded condition for the operator.
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting == nil {
				continue
			}
			if state := containerStatus.State.Waiting; len(state.Reason) > 0 {
				condition := operatorv1.OperatorCondition{
					Type:    "InstallerPodContainerWaitingDegraded",
					Reason:  state.Reason,
					Status:  operatorv1.ConditionTrue,
					Message: fmt.Sprintf("Pod %q container %q is waiting for %s because %s", pod.Name, containerStatus.Name, pendingTime, state.Message),
				}
				conditions = append(conditions, condition)
				c.eventRecorder.Warningf(condition.Reason, condition.Message)
			}
		}
	}

	return conditions
}

func (c *InstallerStateController) checkPendingInstallerPod(pod *v1.Pod) error {
	return nil
}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *InstallerStateController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting InstallerStateController")
	defer klog.Infof("Shutting down InstallerStateController")
	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *InstallerStateController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *InstallerStateController) processNextWorkItem() bool {
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
