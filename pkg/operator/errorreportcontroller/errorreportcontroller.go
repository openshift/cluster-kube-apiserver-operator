package errorreportcontroller

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/klog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1informers "k8s.io/client-go/informers/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

var (
	messageToRootCauseMap = map[string]string{
		"failed to create pod network sandbox": "NetworkError",
	}
)

// FIXME: description: ErrorReportController is a controller that sets upgradeable=false if anything outside the whitelist is the specified featuregates.
type ErrorReportController struct {
	operatorClient v1helpers.OperatorClient
	eventsLister   corev1listers.EventNamespaceLister

	regex *regexp.Regexp

	cachesToSync  []cache.InformerSynced
	eventRecorder events.Recorder
}

func NewErrorReportController(
	operatorClient v1helpers.OperatorClient,
	coreInformer corev1informers.EventInformer,
	eventRecorder events.Recorder,
) *ErrorReportController {
	c := &ErrorReportController{
		operatorClient: operatorClient,
		eventsLister:   coreInformer.Lister().Events("openshift-kube-apiserver"),
		eventRecorder:  eventRecorder.WithComponentSuffix("error-reporter"),
	}

	// create a regular expression as (cond1|cond2|...) so that we can directly match message to condition reason

	c.regex = constructCumulativeRegex(messageToRootCauseMap)
	c.cachesToSync = append(c.cachesToSync, operatorClient.Informer().HasSynced, coreInformer.Informer().HasSynced)
	return c
}

func (c *ErrorReportController) sync() error {
	targetEvents, err := c.eventsLister.List(labels.NewSelector())
	if err != nil {
		return err
	}

	cond := checkForPodCreationErrors(c.regex, targetEvents)
	if _, _, updateError := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(cond)); updateError != nil {
		return updateError
	}

	return nil
}

func constructCumulativeRegex(errorToReasonMap map[string]string) *regexp.Regexp {
	regexString := "("
	for re := range errorToReasonMap {
		regexString += re + "|"
	}
	// replace last "|" with ")"
	regexString = regexString[:len(regexString)-1] + ")"

	return regexp.MustCompile(regexString)
}

func checkForPodCreationErrors(errorsRegex *regexp.Regexp, events []*corev1.Event) operatorv1.OperatorCondition {
	reasons := sets.NewString()

	for _, e := range events {
		if e.Reason != "FailedCreatePodSandBox" {
			continue
		}

		// don't include the messages, we just want to be able to see what the issue is right away from the status
		if matches := errorsRegex.FindStringSubmatch(e.Message); len(matches) > 1 {
			reason, found := messageToRootCauseMap[matches[1]]
			if !found {
				klog.Errorf("couldn't find the reason for match: %s", matches[1])
			} else {
				reasons.Insert(reason)
			}
		}
	}

	if reasons.Len() > 0 {
		return operatorv1.OperatorCondition{
			Type:   "StaticPodCreationDegraded",
			Reason: strings.Join(reasons.List(), "_"),
			Status: operatorv1.ConditionTrue,
		}
	}

	return operatorv1.OperatorCondition{
		Type:   "StaticPodCreationDegraded",
		Reason: "NoWatchedErrorsAppeared",
		Status: operatorv1.ConditionFalse,
	}

}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *ErrorReportController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	klog.Infof("Starting ErrorReportController")
	defer klog.Infof("Shutting down ErrorReportController")
	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *ErrorReportController) runWorker() {
	for c.processNextWorkItem() {
		time.Sleep(5 * time.Second)
	}
}

func (c *ErrorReportController) processNextWorkItem() bool {
	err := c.sync()
	if err == nil {
		return true
	}

	utilruntime.HandleError(fmt.Errorf("sync() failed with : %v", err))

	return true
}
