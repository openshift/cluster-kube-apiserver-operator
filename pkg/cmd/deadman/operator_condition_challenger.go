package deadman

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configv1versionedtypes "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	configv1lister "github.com/openshift/client-go/config/listers/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type OperatorConditionChallenger struct {
	// operatorToTimeToCheckForStaleness is a map from operator name to the time at which the condition messages will be
	// reset to a "checking for activity" message.
	operatorToTimeToCheckForStaleness map[string]time.Time

	durationAllowedBetweenStalenessChecks time.Duration

	clusterOperatorClient configv1versionedtypes.ClusterOperatorInterface
	clusterOperatorLister configv1lister.ClusterOperatorLister
	eventRecorder         events.Recorder

	queue workqueue.RateLimitingInterface
}

// NewOperatorConditionChallenger is a controller that sets operator condition messages to ensure that the operator
// in question updates the condition messages back.  If the condition messages are not changed, then the operator
// condition is considered stale.
func NewOperatorConditionChallenger(
	configClient configeversionedclient.Interface,
	configInformers configv1informers.SharedInformerFactory,
	durationAllowedBetweenStalenessChecks time.Duration,
	eventRecorder events.Recorder,
) *OperatorConditionChallenger {

	c := &OperatorConditionChallenger{
		operatorToTimeToCheckForStaleness:     map[string]time.Time{},
		durationAllowedBetweenStalenessChecks: durationAllowedBetweenStalenessChecks,

		clusterOperatorClient: configClient.ConfigV1().ClusterOperators(),
		clusterOperatorLister: configInformers.Config().V1().ClusterOperators().Lister(),
		eventRecorder:         eventRecorder,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "OperatorConditionChallenger"),
	}

	configInformers.Config().V1().ClusterOperators().Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				clusterOperator := obj.(*configv1.ClusterOperator)
				c.queue.Add(clusterOperator.Name)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				clusterOperator := newObj.(*configv1.ClusterOperator)
				c.queue.Add(clusterOperator.Name)
			},
		},
		1*time.Minute,
	)

	return c
}

func getMostRecentConditionTransitionTime(clusterOperator *configv1.ClusterOperator) *time.Time {
	if len(clusterOperator.Status.Conditions) == 0 {
		return nil
	}

	newestTime := time.Time{}
	for _, condition := range clusterOperator.Status.Conditions {
		if condition.LastTransitionTime.After(newestTime) {
			newestTime = condition.LastTransitionTime.Time
		}
	}
	return &newestTime
}

func (c *OperatorConditionChallenger) syncHandler(ctx context.Context, key string) error {
	clusterOperator, err := c.clusterOperatorLister.Get(key)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if isCheckingForStaleness(clusterOperator) || isMarkedAsStale(clusterOperator) {
		// staleness check is in progress already, just return
		// operator was marked as stale already, just return
		return nil
	}

	if clusteroperatorhelpers.IsStatusConditionTrue(clusterOperator.Status.Conditions, "Disabled") {
		// some operator use this fake condition to indicate they aren't doing work.  Skip in that case.
		return nil
	}

	mostRecentOperatorUpdate := getMostRecentConditionTransitionTime(clusterOperator)
	if mostRecentOperatorUpdate == nil {
		// this means we have no conditions, so there are no conditions we can challenge the operator for.
		return nil
	}
	nextCheckTimeBasedOnOperatorUpdate := mostRecentOperatorUpdate.Add(c.durationAllowedBetweenStalenessChecks)

	// check to see if the next time to check for activity needs to be updated based on the data.
	cachedNextCheckTime, ok := c.operatorToTimeToCheckForStaleness[clusterOperator.Name]
	switch {
	case !ok:
		c.operatorToTimeToCheckForStaleness[clusterOperator.Name] = nextCheckTimeBasedOnOperatorUpdate

	case cachedNextCheckTime.Before(nextCheckTimeBasedOnOperatorUpdate):
		c.operatorToTimeToCheckForStaleness[clusterOperator.Name] = nextCheckTimeBasedOnOperatorUpdate

	}

	now := time.Now()
	nextLivenessCheckTime := c.operatorToTimeToCheckForStaleness[clusterOperator.Name]
	if now.Before(nextLivenessCheckTime) {
		return nil
	}

	// if we're past the deadline since our last check, so we will perform a staleness check by resetting the message.
	// only modify the message to avoid changing machine-readable reasons or status in the common case when operators
	// are properly managing their status.
	clusterOperatorToWriteAsStale := clusterOperator.DeepCopy()
	for i, condition := range clusterOperator.Status.Conditions {
		clusterOperatorToWriteAsStale.Status.Conditions[i].Message = "Checking for stale status, the active operator will reset this message: " + condition.Message
	}

	klog.V(2).Infof("Challenging clusteroperator/%v for status write", clusterOperator.Name)
	// don't use apply here because we want to conflict if someone else has written status.
	if _, err := c.clusterOperatorClient.UpdateStatus(ctx, clusterOperatorToWriteAsStale, metav1.UpdateOptions{}); err != nil {
		return err
	}

	// mark the next time we should check for staleness
	c.operatorToTimeToCheckForStaleness[clusterOperator.Name] = now.Add(c.durationAllowedBetweenStalenessChecks)

	return nil
}

// Run starts the controller and blocks until the context is closed.
func (c *OperatorConditionChallenger) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting OperatorConditionChallenger")
	defer klog.Infof("Shutting down OperatorConditionChallenger")

	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
}

func (c *OperatorConditionChallenger) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *OperatorConditionChallenger) processNextWorkItem(ctx context.Context) bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.syncHandler(ctx, dsKey.(string))
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}
