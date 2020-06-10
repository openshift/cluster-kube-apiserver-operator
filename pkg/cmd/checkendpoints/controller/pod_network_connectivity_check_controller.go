package controller

import (
	"context"
	"fmt"
	"net"
	"time"

	operatorcontrolplanev1alpha1 "github.com/openshift/api/operatorcontrolplane/v1alpha1"
	operatorcontrolplaneclientv1alpha1 "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned/typed/operatorcontrolplane/v1alpha1"
	alpha1 "github.com/openshift/client-go/operatorcontrolplane/informers/externalversions/operatorcontrolplane/v1alpha1"
	"github.com/openshift/client-go/operatorcontrolplane/listers/operatorcontrolplane/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/operatorcontrolplane/podnetworkconnectivitycheck/v1alpha1helpers"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/trace"
)

// PodNetworkConnectivityCheckController continuously performs network connectivity
// checks and reports the results.
type PodNetworkConnectivityCheckController interface {
	factory.Controller
}

// controller implements a PodNetworkConnectivityCheckController that discovers the list of endpoints to
// check by looking for PodNetworkConnectivityChecks in a given namespace, for a specific pod. Updates to
// the PodNetworkConnectivityCheck status are queued up and handled asynchronously such that disruptions
// to the ability to update the PodNetworkConnectivityCheck status do not disrupt the ability to perform
// the connectivity checks.
type controller struct {
	factory.Controller
	podName      string
	podNamespace string
	checksGetter operatorcontrolplaneclientv1alpha1.PodNetworkConnectivityCheckInterface
	checkLister  v1alpha1.PodNetworkConnectivityCheckNamespaceLister
	recorder     events.Recorder
	// each PodNetworkConnectivityCheck gets its own PodNetworkConnectivityCheckStatusUpdater
	updaters map[string]ConnectionChecker
}

// Returns a new PodNetworkConnectivityCheckController that performs network connectivity checks
// as specified in the PodNetworkConnectivityChecks defined in the specified namespace, for the specified pod.
func NewPodNetworkConnectivityCheckController(podName, podNamespace string, checksGetter operatorcontrolplaneclientv1alpha1.PodNetworkConnectivityChecksGetter, checkInformer alpha1.PodNetworkConnectivityCheckInformer, recorder events.Recorder) PodNetworkConnectivityCheckController {
	c := &controller{
		podName:      podName,
		podNamespace: podNamespace,
		checksGetter: checksGetter.PodNetworkConnectivityChecks(podNamespace),
		checkLister:  checkInformer.Lister().PodNetworkConnectivityChecks(podNamespace),
		recorder:     recorder,
		updaters:     map[string]ConnectionChecker{},
	}
	c.Controller = factory.New().
		WithSync(c.Sync).
		WithBareInformers(checkInformer.Informer()).
		ResyncEvery(1*time.Second).
		ToController("check-endpoints", recorder)
	return c
}

// Sync ensures that the status updaters for each PodNetworkConnectivityCheck is started
// and then performs each check.
func (c *controller) Sync(ctx context.Context, syncContext factory.SyncContext) error {
	checkList, err := c.checkLister.List(labels.Everything())
	if err != nil {
		return err
	}

	// filter list of checks for current pod
	var checks []*operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck
	for _, check := range checkList {
		if check.Spec.SourcePod == c.podName {
			checks = append(checks, check)
		}
	}

	// create & start status updaters if needed
	for _, check := range checks {
		if updater := c.updaters[check.Name]; updater == nil {
			c.updaters[check.Name] = NewConnectionChecker(check, c, c.recorder)
			c.updaters[check.Name].Run(ctx)
		}
	}

	// stop & delete unneeded status updaters
	for name, updater := range c.updaters {
		var keep bool
		for _, check := range checks {
			if check.Name == name {
				keep = true
				break
			}
		}
		if !keep {
			updater.Stop()
			delete(c.updaters, name)
		}
	}

	return nil
}

// Get implements PodNetworkConnectivityCheckClient
func (c *controller) Get(name string) (*operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck, error) {
	return c.checkLister.Get(name)
}

// UpdateStatus implements v1alpha1helpers.PodNetworkConnectivityCheckClient
func (c *controller) UpdateStatus(ctx context.Context, check *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck, opts metav1.UpdateOptions) (*operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck, error) {
	return c.checksGetter.UpdateStatus(ctx, check, opts)
}

// isDNSError returns true if the cause of the net operation error is a DNS error
func isDNSError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if _, ok := opErr.Err.(*net.DNSError); ok {
			return true
		}
	}
	return false
}

// manageStatusLogs returns a status update function that updates the PodNetworkConnectivityCheck.Status's
// Successes/Failures logs reflect the results of the check.
func manageStatusLogs(address string, checkErr error, latency *trace.LatencyInfo) []v1alpha1helpers.UpdateStatusFunc {
	var statusUpdates []v1alpha1helpers.UpdateStatusFunc
	host, _, _ := net.SplitHostPort(address)
	if isDNSError(checkErr) {
		klog.V(2).Infof("%7s | %-15s | %6dms | Failure looking up host %s: %v", "Failure", "DNSError", latency.DNS.Milliseconds(), host, checkErr)
		return append(statusUpdates, v1alpha1helpers.AddFailureLogEntry(operatorcontrolplanev1alpha1.LogEntry{
			Start:   metav1.NewTime(latency.DNSStart),
			Success: false,
			Reason:  operatorcontrolplanev1alpha1.LogEntryReasonDNSError,
			Message: fmt.Sprintf("Failure looking up host %s: %v", host, checkErr),
			Latency: metav1.Duration{},
		}))
	}
	if latency.DNS != 0 {
		klog.V(2).Infof("%7s | %-15s | %6dms | Resolved host name %s successfully", "Success", "DNSResolve", latency.DNS.Milliseconds(), host)
		statusUpdates = append(statusUpdates, v1alpha1helpers.AddSuccessLogEntry(operatorcontrolplanev1alpha1.LogEntry{
			Start:   metav1.NewTime(latency.DNSStart),
			Success: true,
			Reason:  operatorcontrolplanev1alpha1.LogEntryReasonDNSError,
			Message: fmt.Sprintf("Resolved host name %s successfully", host),
			Latency: metav1.Duration{},
		}))
	}
	if checkErr != nil {
		klog.V(2).Infof("%7s | %-15s | %6dms | Failed to establish a TCP connection to %s: %v", "Failure", "TCPConnectError", latency.Connect.Milliseconds(), address, checkErr)
		return append(statusUpdates, v1alpha1helpers.AddFailureLogEntry(operatorcontrolplanev1alpha1.LogEntry{
			Start:   metav1.NewTime(latency.ConnectStart),
			Success: false,
			Reason:  operatorcontrolplanev1alpha1.LogEntryReasonTCPConnectError,
			Message: fmt.Sprintf("Failed to establish a TCP connection to %s: %v", address, checkErr),
			Latency: metav1.Duration{Duration: latency.Connect},
		}))
	}
	klog.V(2).Infof("%7s | %-15s | %6dms | TCP connection to %v succeeded", "Success", "TCPConnect", latency.Connect.Milliseconds(), address)
	return append(statusUpdates, v1alpha1helpers.AddSuccessLogEntry(operatorcontrolplanev1alpha1.LogEntry{
		Start:   metav1.NewTime(latency.ConnectStart),
		Success: true,
		Reason:  operatorcontrolplanev1alpha1.LogEntryReasonTCPConnect,
		Message: fmt.Sprintf("TCP connection to %s succeeded.", address),
		Latency: metav1.Duration{Duration: latency.Connect},
	}))
}

// manageStatusOutage returns a status update function that manages the
// PodNetworkConnectivityCheck.Status entries based on Successes/Failures log entries.
func manageStatusOutage(recorder events.Recorder) v1alpha1helpers.UpdateStatusFunc {
	return func(status *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheckStatus) {
		var currentOutage *operatorcontrolplanev1alpha1.OutageEntry
		if len(status.Outages) > 0 && status.Outages[0].End.IsZero() {
			currentOutage = &status.Outages[0]
		}
		var latestFailure, latestSuccess operatorcontrolplanev1alpha1.LogEntry
		if len(status.Failures) > 0 {
			latestFailure = status.Failures[0]
		}
		if len(status.Successes) > 0 {
			latestSuccess = status.Successes[0]
		}
		if currentOutage == nil {
			if latestFailure.Start.After(latestSuccess.Start.Time) {
				recorder.Warningf("ConnectivityOutageDetected", "Connectivity outage detected: %s", latestFailure.Message)
				status.Outages = append([]operatorcontrolplanev1alpha1.OutageEntry{{Start: latestFailure.Start}}, status.Outages...)
			}
		} else {
			if latestSuccess.Start.After(latestFailure.Start.Time) {
				recorder.Eventf("ConnectivityRestored", "Connectivity restored: %s", latestSuccess.Message)
				currentOutage.End = latestSuccess.Start
			}
		}
	}
}

// manageStatusConditions returns a status update function that set the appropriate conditions on the
// PodNetworkConnectivityCheck.
func manageStatusConditions(status *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheckStatus) {
	reachableCondition := operatorcontrolplanev1alpha1.PodNetworkConnectivityCheckCondition{
		Type:   operatorcontrolplanev1alpha1.Reachable,
		Status: metav1.ConditionUnknown,
	}
	if len(status.Outages) == 0 || !status.Outages[0].End.IsZero() {
		var latestSuccessLogEntry operatorcontrolplanev1alpha1.LogEntry
		if len(status.Successes) > 0 {
			latestSuccessLogEntry = status.Successes[0]
		}
		reachableCondition.Status = metav1.ConditionTrue
		reachableCondition.Reason = "TCPConnectSuccess"
		reachableCondition.Message = latestSuccessLogEntry.Message
	} else {
		var latestFailureLogEntry operatorcontrolplanev1alpha1.LogEntry
		if len(status.Failures) > 0 {
			latestFailureLogEntry = status.Failures[0]
		}
		reachableCondition.Status = metav1.ConditionFalse
		reachableCondition.Reason = latestFailureLogEntry.Reason
		reachableCondition.Message = latestFailureLogEntry.Message
	}
	v1alpha1helpers.SetPodNetworkConnectivityCheckCondition(&status.Conditions, reachableCondition)
}
