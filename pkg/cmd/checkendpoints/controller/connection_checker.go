package controller

import (
	"context"
	"net"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/trace"

	operatorcontrolplanev1alpha1 "github.com/openshift/api/operatorcontrolplane/v1alpha1"

	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/operatorcontrolplane/podnetworkconnectivitycheck/v1alpha1helpers"
)

// ConnectionChecker checks a single connection and updates status when appropriate
type ConnectionChecker interface {
	Run(ctx context.Context)
	Stop()
}

// NewConnectionChecker returns a ConnectionChecker.
func NewConnectionChecker(check *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck, client v1alpha1helpers.PodNetworkConnectivityCheckClient, recorder events.Recorder) ConnectionChecker {
	return &connectionChecker{
		check:    check,
		client:   client,
		recorder: recorder,
		stop:     make(chan interface{}),
	}
}

type connectionChecker struct {
	check       *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck
	client      v1alpha1helpers.PodNetworkConnectivityCheckClient
	recorder    events.Recorder
	updatesLock sync.Mutex
	updates     []v1alpha1helpers.UpdateStatusFunc
	stop        chan interface{}
}

// Add updates to the list.
func (c *connectionChecker) add(updates ...v1alpha1helpers.UpdateStatusFunc) {
	c.updatesLock.Lock()
	defer c.updatesLock.Unlock()
	c.updates = append(c.updates, updates...)
}

func (c *connectionChecker) checkConnection(ctx context.Context) {
	for {
		select {
		case <-c.stop:
			return
		case <-ctx.Done():
			return

		case <-time.After(1 * time.Second):
			go func() {
				c.checkEndpoint(ctx, c.check)
				c.updateStatus(ctx)
			}()
		}
	}
}

// Start the apply/retry loop.
func (c *connectionChecker) Run(ctx context.Context) {
	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		c.checkConnection(ctx)
	}, 1*time.Second)

	<-ctx.Done() // wait for controller context to be cancelled
}

// Stop
func (c *connectionChecker) Stop() {
	close(c.stop)
}

func (c *connectionChecker) updateStatus(ctx context.Context) {
	c.updatesLock.Lock()
	defer c.updatesLock.Unlock()
	if len(c.updates) > 20 {
		_, _, err := v1alpha1helpers.UpdateStatus(ctx, c.client, c.check.Name, c.updates...)
		if err != nil {
			klog.Warningf("Unable to update %s: %v", c.check.Name, err)
			c.recorder.Warningf("UpdateFailed", "Unable to update %s: %v", c.check.Name, err)
			return
		}
		c.updates = nil
	}
}

// checkEndpoint performs the check and manages the PodNetworkConnectivityCheck.Status changes that result.
func (c *connectionChecker) checkEndpoint(ctx context.Context, check *operatorcontrolplanev1alpha1.PodNetworkConnectivityCheck) {
	latencyInfo, err := getTCPConnectLatency(ctx, check.Spec.TargetEndpoint)
	statusUpdates := manageStatusLogs(check.Spec.TargetEndpoint, err, latencyInfo)
	if len(statusUpdates) > 0 {
		statusUpdates = append(statusUpdates, manageStatusOutage(c.recorder))
	}
	if len(statusUpdates) > 0 {
		statusUpdates = append(statusUpdates, manageStatusConditions)
	}
	c.add(statusUpdates...)
}

// getTCPConnectLatency connects to a tcp endpoint and collects latency info
func getTCPConnectLatency(ctx context.Context, address string) (*trace.LatencyInfo, error) {
	klog.V(3).Infof("Check BEGIN: %v", address)
	defer klog.V(3).Infof("Check END  : %v", address)
	ctx, latencyInfo := trace.WithLatencyInfoCapture(ctx)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err == nil {
		conn.Close()
	}
	return latencyInfo, err
}
