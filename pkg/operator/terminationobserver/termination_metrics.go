package terminationobserver

import (
	"github.com/blang/semver"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

type terminationMetrics struct {
	store           *memoryStorage
	apiServerMetric *prometheus.GaugeVec
}

// RegisterMetrics registers the termination metrics into legacy prometheus registry.
func RegisterMetrics(store *memoryStorage) {
	legacyregistry.MustRegister(newTerminationMetrics(store))
}

func newTerminationMetrics(store *memoryStorage) *terminationMetrics {
	return &terminationMetrics{
		store: store,
		apiServerMetric: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "openshift_kube_apiserver_termination_event_time",
			Help: "Report times of termination events observed for individual API server instances",
		}, []string{"name", "eventName"}),
	}
}

var _ metrics.Registerable = &terminationMetrics{}

func (t *terminationMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- t.apiServerMetric.WithLabelValues("", "").Desc()
}

func (t *terminationMetrics) Collect(ch chan<- prometheus.Metric) {
	servers := t.store.ListNames()
	for _, serverName := range servers {
		state := t.store.Get(serverName)
		if state == nil || state.terminationTimestamp == nil {
			continue
		}

		// StaticPodReplaced is fake event that indicates time when the API server was observed as replaced.
		// We always report this.
		sample := t.apiServerMetric.WithLabelValues(serverName, "StaticPodTerminated")
		sample.Set(float64(state.createdTimestamp.Unix()))
		ch <- sample

		// Report all termination events and times they were observed.
		for _, terminationReason := range terminationEventReasons {
			value := float64(0)
			sample := t.apiServerMetric.WithLabelValues(serverName, terminationReason)
			event := t.store.GetEventWithReason(serverName, terminationReason)
			if event != nil {
				value = float64(event.LastTimestamp.Unix())
			}
			sample.Set(value)
			ch <- sample
		}
	}
}

func (t *terminationMetrics) Create(*semver.Version) bool {
	return true
}
