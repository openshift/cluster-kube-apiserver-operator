package controller

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/operatorcontrolplane/podnetworkconnectivitycheck/v1alpha1helpers"
)

// NewUpdatesManager returns a queue sorting UpdateManager with the specified sorting window.
func NewUpdatesManager(window time.Duration, processor UpdatesProcessor) UpdatesManager {
	return &updatesManager{
		window:       window,
		sortingQueue: map[time.Time][]v1alpha1helpers.UpdateStatusFunc{},
		processor:    processor,
	}
}

// UpdatesManager manages a queue of updates.
type UpdatesManager interface {
	// Add an update to the queue.
	Add(timestamp time.Time, updates ...v1alpha1helpers.UpdateStatusFunc)
	// Process the updates ready to be processed.
	Process(ctx context.Context, flush bool) error
}

// updateManage implements an UpdateManager sorts the incoming updates and queues updates
// outside of a sorting window, anchored on one end by the latest update, for processing.
type updatesManager struct {
	// lock for queues
	lock sync.Mutex
	// how long to wait for an out-of-order update
	window time.Duration
	// sortingQueue holds for sorting during the sorting window.
	sortingQueue map[time.Time][]v1alpha1helpers.UpdateStatusFunc
	// order of updates in the sortingQueue
	timestamps []time.Time
	// updates ready to be processed
	processingQueue []v1alpha1helpers.UpdateStatusFunc
	// processor of updates
	processor UpdatesProcessor
}

type UpdatesProcessor func(context.Context, ...v1alpha1helpers.UpdateStatusFunc) error

// Add an update to the queue. There is a delay equal to the size of the sorting window before
// updates are made available on the queue to allow for updates submitted out of order within
// the sorting window to be sorted by timestamp.
func (u *updatesManager) Add(timestamp time.Time, updates ...v1alpha1helpers.UpdateStatusFunc) {
	u.lock.Lock()
	defer u.lock.Unlock()

	u.sortingQueue[timestamp] = updates

	u.timestamps = append(u.timestamps, timestamp)
	sort.Slice(u.timestamps, func(i, j int) bool {
		return u.timestamps[i].Before(u.timestamps[j])
	})

	latestTimestamp := u.timestamps[len(u.timestamps)-1]
	var tmp []time.Time
	for _, timestamp := range u.timestamps {
		if latestTimestamp.Sub(timestamp) > u.window {
			// move updates not in the sorting window to the processing queue
			u.processingQueue = append(u.processingQueue, u.sortingQueue[timestamp]...)
			delete(u.sortingQueue, timestamp)
			continue
		}
		// only updates within the window remain
		tmp = append(tmp, timestamp)
	}
	u.timestamps = tmp
}

// Process updates and remove from the processing queue. If flush is true, updates not
// ready to be processed are also processed.
func (u *updatesManager) Process(ctx context.Context, flush bool) error {
	u.lock.Lock()
	defer u.lock.Unlock()
	if flush || len(u.processingQueue) > 20 {
		if err := u.processor(ctx, u.processingQueue...); err != nil {
			return err
		}
		u.processingQueue = nil
	}
	if flush {
		var updates []v1alpha1helpers.UpdateStatusFunc
		for _, timestamp := range u.timestamps {
			updates = append(updates, u.sortingQueue[timestamp]...)
		}
		if err := u.processor(ctx, updates...); err != nil {
			return err
		}
		u.sortingQueue = map[time.Time][]v1alpha1helpers.UpdateStatusFunc{}
		u.timestamps = nil
	}
	return nil
}
