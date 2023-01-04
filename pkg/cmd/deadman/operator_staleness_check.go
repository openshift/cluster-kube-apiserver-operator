package deadman

import (
	"context"
	"fmt"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configv1versionedtypes "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	configv1lister "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type OperatorStalenessChecker struct {
	// operatorToDeadlineForResponse is a map from operator name to the time at which the condition will be considered stale
	// and the condition will be reset to unknown.
	operatorToDeadlineForResponse map[string]time.Time

	durationAllowedForOperatorResponse time.Duration

	clusterOperatorClient configv1versionedtypes.ClusterOperatorInterface
	clusterOperatorLister configv1lister.ClusterOperatorLister
	eventRecorder         events.Recorder

	// for unit testing
	now   func() time.Time
	queue workqueue.RateLimitingInterface
}

// NewOperatorStalenessChecker is a controller that sets operator conditions status, reason, and messages when the operator
// fails to respond to a challenge and correct a message.
func NewOperatorStalenessChecker(
	configClient configeversionedclient.Interface,
	configInformers configv1informers.SharedInformerFactory,
	durationAllowedForOperatorResponse time.Duration,
	eventRecorder events.Recorder,
) *OperatorStalenessChecker {

	c := &OperatorStalenessChecker{
		operatorToDeadlineForResponse:      map[string]time.Time{},
		durationAllowedForOperatorResponse: durationAllowedForOperatorResponse,

		clusterOperatorClient: configClient.ConfigV1().ClusterOperators(),
		clusterOperatorLister: configInformers.Config().V1().ClusterOperators().Lister(),
		eventRecorder:         eventRecorder,

		now:   time.Now,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "OperatorStalenessChecker"),
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

func isCheckingForStaleness(clusterOperator *configv1.ClusterOperator) bool {
	for _, condition := range clusterOperator.Status.Conditions {
		if strings.HasPrefix(condition.Message, "Checking for stale status") {
			return true
		}
	}

	return false
}

func isMarkedAsStale(clusterOperator *configv1.ClusterOperator) bool {
	for _, condition := range clusterOperator.Status.Conditions {
		if strings.HasPrefix(condition.Message, "Operator has not fixed status in at least") {
			return true
		}
	}

	return false
}

func (c *OperatorStalenessChecker) syncHandler(ctx context.Context, key string) error {
	clusterOperator, err := c.clusterOperatorLister.Get(key)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if !isCheckingForStaleness(clusterOperator) {
		delete(c.operatorToDeadlineForResponse, clusterOperator.Name)
		return nil
	}

	now := c.now()
	if _, ok := c.operatorToDeadlineForResponse[clusterOperator.Name]; !ok {
		c.operatorToDeadlineForResponse[clusterOperator.Name] = now.Add(c.durationAllowedForOperatorResponse)
	}
	deadlineForReset := c.operatorToDeadlineForResponse[clusterOperator.Name]
	if now.Before(deadlineForReset) {
		return nil
	}

	// if we're past the deadline and some messsages still indicate we're checking for staleness, every condition should
	// be marked as unknown and indicated as stale.
	clusterOperatorToWriteAsStale := clusterOperator.DeepCopy()
	for i, condition := range clusterOperator.Status.Conditions {
		switch condition.Type {
		case configv1.OperatorDegraded:
			// if the operator is not setting status, it is Degraded.
			clusterOperatorToWriteAsStale.Status.Conditions[i].Status = configv1.ConditionTrue
		case configv1.OperatorUpgradeable:
			if condition.Status == configv1.ConditionFalse {
				// if the cluster isn't upgradeable, be sure not to suddenly allow improper upgrades
			} else {
				clusterOperatorToWriteAsStale.Status.Conditions[i].Status = configv1.ConditionUnknown
			}

		default:
			// The other conditions are Unknown.
			clusterOperatorToWriteAsStale.Status.Conditions[i].Status = configv1.ConditionUnknown
		}

		if condition.Status != clusterOperatorToWriteAsStale.Status.Conditions[i].Status {
			clusterOperatorToWriteAsStale.Status.Conditions[i].LastTransitionTime = metav1.Time{Time: now}
		}
		clusterOperatorToWriteAsStale.Status.Conditions[i].Reason = "OperatorFailedStalenessCheck"
		clusterOperatorToWriteAsStale.Status.Conditions[i].Message = fmt.Sprintf("Operator has not fixed status in at least %v.  Last reason was %q, last status was: %v", c.durationAllowedForOperatorResponse, condition.Reason, condition.Message)
	}

	msg := fmt.Sprintf("clusteroperator/%v has not fixed status in at least %v, marking stale and degraded.", clusterOperator.Name, c.durationAllowedForOperatorResponse)
	klog.Warning(msg)
	c.eventRecorder.Warning("OperatorFailedStalenessCheck", msg)
	// don't use apply here because we want to conflict if someone else has written status.
	if _, err := c.clusterOperatorClient.UpdateStatus(ctx, clusterOperatorToWriteAsStale, metav1.UpdateOptions{}); err != nil {
		return err
	}
	delete(c.operatorToDeadlineForResponse, clusterOperator.Name)

	return nil
}

// Run starts the controller and blocks until the context is closed.
func (c *OperatorStalenessChecker) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting OperatorStalenessChecker")
	defer klog.Infof("Shutting down OperatorStalenessChecker")

	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
}

func (c *OperatorStalenessChecker) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *OperatorStalenessChecker) processNextWorkItem(ctx context.Context) bool {
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
