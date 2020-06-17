package terminationobserver

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/component-base/metrics"
)

// ProcessLateConnectionEvents increment openshift_kube_apiserver_lateconnections_count counter for apiserver reported in LateConnections event.
// The apiserver received connections very late in the graceful termination process, possibly a sign for a broken load balancer setup.
func ProcessLateConnectionEvents(event *v1.Event) error {
	// best-effort to guess the source (apiserver) from event
	name := event.InvolvedObject.Name
	if len(name) == 0 {
		name = event.Source.Component
	}
	if len(name) == 0 {
		name = event.Source.Host
	}
	apiServerLateConnectionsCounter.WithLabelValues(event.InvolvedObject.Name).Inc()
	return nil
}

var (
	apiServerLateConnectionsCounter = metrics.NewCounterVec(&metrics.CounterOpts{
		Name: "openshift_kube_apiserver_lateconnections_count",
		Help: "Report observed late connection count for each API server instance over time",
	}, []string{"name"})
)
