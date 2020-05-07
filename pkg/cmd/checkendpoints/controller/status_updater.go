package controller

import (
	"context"
	"sync"
	"time"

	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/operatorcontrolplane/podnetworkconnectivitycheck/v1alpha1helpers"
)

// PodNetworkConnectivityCheckStatusUpdater maintains a slice of status updates to apply
// to a PodNetworkConnectivityCheckClient resource. Problems that occur when applying the
// updates are logged and the updates are retried.
type PodNetworkConnectivityCheckStatusUpdater interface {
	Start(ctx context.Context)
	Stop()
	Add(updates ...v1alpha1helpers.UpdateStatusFunc)
}

// NewPodNetworkConnectivityCheckStatusUpdater returns a PodNetworkConnectivityCheckStatusUpdater.
func NewPodNetworkConnectivityCheckStatusUpdater(client v1alpha1helpers.PodNetworkConnectivityCheckClient, checkName string, recorder events.Recorder) PodNetworkConnectivityCheckStatusUpdater {
	return &statusUpdater{
		checkName: checkName,
		client:    client,
		recorder:  recorder,
		stop:      make(chan interface{}),
	}
}

type statusUpdater struct {
	checkName   string
	client      v1alpha1helpers.PodNetworkConnectivityCheckClient
	recorder    events.Recorder
	updatesLock sync.Mutex
	updates     []v1alpha1helpers.UpdateStatusFunc
	stop        chan interface{}
}

// Add updates to the list.
func (s *statusUpdater) Add(updates ...v1alpha1helpers.UpdateStatusFunc) {
	s.updatesLock.Lock()
	defer s.updatesLock.Unlock()
	s.updates = append(s.updates, updates...)
}

// Start the apply/retry loop.
func (s *statusUpdater) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-s.stop:
				return
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				s.updateStatus(ctx)
			}
		}
	}()
}

// Stop
func (s *statusUpdater) Stop() {
	close(s.stop)
}

func (s *statusUpdater) updateStatus(ctx context.Context) {
	s.updatesLock.Lock()
	defer s.updatesLock.Unlock()
	if len(s.updates) > 0 {
		_, _, err := v1alpha1helpers.UpdateStatus(ctx, s.client, s.checkName, s.updates...)
		if err != nil {
			klog.Warningf("Unable to update %s: %v", s.checkName, err)
			s.recorder.Warningf("UpdateFailed", "Unable to update %s: %v", s.checkName, err)
			return
		}
		s.updates = nil
	}
}
