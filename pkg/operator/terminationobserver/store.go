package terminationobserver

import (
	"errors"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// memoryStorage is simple, synchronized in-memory storage that track termination time and creation
// time and events for each individual API server static pod.
// The storage is shared between observer that populate it and Prometheus metrics collector.
type memoryStorage struct {
	sync.RWMutex
	state map[string]apiServerState
}

type apiServerState struct {
	// createdTimestamp is a 'creationTimestamp' of currently running API server static pod.
	createdTimestamp time.Time

	// terminationTimestamp is a 'creationTimestamp' of previously running API server static pod.
	terminationTimestamp *time.Time

	// terminationEvents are termination events collected for the previously running API server static pod.
	terminationEvents []*corev1.Event
}

// NewStorage initialize empty storage.
func NewStorage() *memoryStorage {
	return &memoryStorage{state: map[string]apiServerState{}}
}

// apiServerNotFoundError is used when the internal storage does not have state for given API server.
var apiServerNotFoundError = errors.New("api server state not found")

// ListNames lists the names of API server in store.
func (m *memoryStorage) ListNames() []string {
	m.RLock()
	defer m.RUnlock()
	var names []string
	for name := range m.state {
		names = append(names, name)
	}
	return names
}

// Get will return the state of the given API server.
func (m *memoryStorage) Get(name string) *apiServerState {
	m.RLock()
	defer m.RUnlock()
	state, ok := m.state[name]
	if !ok {
		return nil
	}
	return &state
}

// GetEventWithReason return event that matches the given reason for the given API server.
// If the event is not observed, return nil.
func (m *memoryStorage) GetEventWithReason(name, reason string) *corev1.Event {
	state := m.Get(name)
	if state == nil {
		return nil
	}
	for i := range state.terminationEvents {
		if state.terminationEvents[i].Reason == reason {
			return state.terminationEvents[i]
		}
	}
	return nil
}

// AddEvent updates the termination events for given API server.
// It also prunes the existing events and remove all events that are older than previous termination.
func (m *memoryStorage) AddEvent(name string, event *corev1.Event) error {
	m.Lock()
	defer m.Unlock()
	state, ok := m.state[name]
	if !ok {
		return apiServerNotFoundError
	}

	// update the state
	newState := apiServerState{
		createdTimestamp:     state.createdTimestamp,
		terminationTimestamp: &state.createdTimestamp,
		terminationEvents:    append(state.terminationEvents, event),
	}
	m.state[name] = newState
	return nil
}

// MarkApiServerTerminating updates the API server state creation timestamp.
// If the server is not tracked, it is added to the store and createdTimestamp is set.
// If the server is already tracked, set the createdTimestamp and copy the old created timestamp to terminationTimestamp.
func (m *memoryStorage) MarkApiServerTerminating(name string, terminationTime time.Time) {
	m.Lock()
	defer m.Unlock()
	oldState, ok := m.state[name]
	if !ok {
		m.state[name] = apiServerState{createdTimestamp: terminationTime}
		return
	}

	// prune existing events, remove everything that is prior terminationTimestamp
	var prunedEvents []*corev1.Event
	for i := range oldState.terminationEvents {
		if oldState.terminationEvents[i].LastTimestamp.Time.Before(*oldState.terminationTimestamp) {
			continue
		}
		prunedEvents = append(prunedEvents, oldState.terminationEvents[i])
	}
	// sort the events from newest to oldest
	sort.Slice(prunedEvents, func(i, j int) bool {
		return prunedEvents[i].LastTimestamp.Time.Before(prunedEvents[j].LastTimestamp.Time)
	})

	newState := apiServerState{
		createdTimestamp:     terminationTime,
		terminationTimestamp: &oldState.createdTimestamp,
		terminationEvents:    prunedEvents,
	}
	m.state[name] = newState
}
