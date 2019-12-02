package terminationobserver

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fakeTerminationEvent(reason string, lastCreated *time.Time) *corev1.Event {
	return &corev1.Event{Reason: reason, LastTimestamp: metav1.Time{Time: *lastCreated}}
}

func TestCollect(t *testing.T) {
	now := time.Now()
	nowAddPtr := func(m int) *time.Time {
		t := now.Add(time.Duration(m) * time.Minute)
		return &t
	}

	tests := []struct {
		name                string
		evaluateCollected   func([]prometheus.Metric)
		expectedSampleCount int
		storage             func() *memoryStorage
		evalMetrics         func(t *testing.T, metrics []*dto.Metric)
	}{
		{
			name:                "two servers with TerminationStart event firing",
			evaluateCollected:   func(metrics []prometheus.Metric) {},
			expectedSampleCount: 2 + len(terminationEventReasons)*2,
			storage: func() *memoryStorage {
				s := NewStorage()
				s.state["server-0"] = apiServerState{
					createdTimestamp:     now,
					terminationTimestamp: nowAddPtr(-10),
					terminationEvents:    []*corev1.Event{fakeTerminationEvent("TerminationStart", nowAddPtr(-9))},
				}
				s.state["server-1"] = apiServerState{
					createdTimestamp:     now,
					terminationTimestamp: nowAddPtr(-10),
					terminationEvents:    []*corev1.Event{fakeTerminationEvent("TerminationStart", nowAddPtr(-8))},
				}
				return s
			},
			evalMetrics: func(t *testing.T, metrics []*dto.Metric) {
				foundTerminated := false
				foundTerminatedStart := false
				foundTerminationStop := false
				for _, m := range metrics {
					for _, l := range m.Label {
						if l.GetName() == "eventName" && l.GetValue() == "StaticPodTerminated" {
							if m.GetGauge().GetValue() == 0 {
								t.Errorf("expected StaticPodTerminated to be set")
							} else {
								foundTerminated = true
							}
						}
						if l.GetName() == "eventName" && l.GetValue() == "TerminationStart" {
							if m.GetGauge().GetValue() == 0 {
								t.Errorf("expected TerminationStart to be set")
							} else {
								foundTerminatedStart = true
							}
						}
						if l.GetName() == "eventName" && l.GetValue() == "TerminationStoppedServing" {
							if m.GetGauge().GetValue() != 0 {
								t.Errorf("expected TerminationStoppedServing not to be set")
							} else {
								foundTerminationStop = true
							}
						}
					}
				}
				if !foundTerminated {
					t.Errorf("expected to find StaticPodTerminated metric")
				}
				if !foundTerminatedStart {
					t.Errorf("expected to find TerminationStart metric")
				}
				if !foundTerminationStop {
					t.Errorf("expected to find TerminationStoppedServing metric")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics := newTerminationMetrics(test.storage())

			stopCh := make(chan struct{})
			collectCh := make(chan prometheus.Metric)
			var collectedMetrics []*dto.Metric

			go func() {
				defer close(stopCh)
				for {
					select {
					case s := <-collectCh:
						m := &dto.Metric{}
						if err := s.Write(m); err != nil {
							t.Fatal(err)
						}
						collectedMetrics = append(collectedMetrics, m)
						if len(collectedMetrics) == test.expectedSampleCount {
							return
						}
					case <-time.After(5 * time.Second):
						t.Error("timeout waiting for all events")
						return
					}
				}
			}()

			metrics.Collect(collectCh)
			<-stopCh
			test.evalMetrics(t, collectedMetrics)
		})
	}
}
