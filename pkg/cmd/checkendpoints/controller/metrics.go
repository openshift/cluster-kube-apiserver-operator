package controller

import (
	"sync"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints/trace"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	registerMetrics sync.Once

	endpointCheckCounter   *metrics.CounterVec
	tcpConnectLatencyGauge *metrics.GaugeVec
	dnsResolveLatencyGauge *metrics.GaugeVec
)

func RegisterMetrics(prefix string) {
	registerMetrics.Do(func() {
		endpointCheckCounter = metrics.NewCounterVec(&metrics.CounterOpts{
			Name: prefix + "endpoint_check_count",
			Help: "Report status of endpoint checks for each pod over time.",
		}, []string{"endpoint", "tcpConnect", "dnsResolve"})

		tcpConnectLatencyGauge = metrics.NewGaugeVec(&metrics.GaugeOpts{
			Name: prefix + "endpoint_check_tcp_connect_latency_gauge",
			Help: "Report latency of TCP connect to endpoint for each pod over time.",
		}, []string{"endpoint"})

		dnsResolveLatencyGauge = metrics.NewGaugeVec(&metrics.GaugeOpts{
			Name: prefix + "endpoint_check_dns_resolve_latency_gauge",
			Help: "Report latency of DNS resolve of endpoint for each pod over time.",
		}, []string{"endpoint"})
		legacyregistry.MustRegister(endpointCheckCounter)
		legacyregistry.MustRegister(tcpConnectLatencyGauge)
		legacyregistry.MustRegister(dnsResolveLatencyGauge)
	})
}

func updateMetrics(address string, latency *trace.LatencyInfo, checkErr error) {
	endpointCheckCounter.With(getCounterMetricLabels(address, latency, checkErr)).Inc()
	if latency.Connect > 0 {
		tcpConnectLatencyGauge.WithLabelValues(address).Set(float64(latency.Connect.Nanoseconds()))
	}
	if latency.DNS > 0 {
		dnsResolveLatencyGauge.WithLabelValues(address).Set(float64(latency.DNS.Nanoseconds()))
	}
}

func getCounterMetricLabels(address string, latency *trace.LatencyInfo, checkErr error) map[string]string {
	labels := map[string]string{
		"endpoint":   address,
		"dnsResolve": "",
		"tcpConnect": "",
	}
	if isDNSError(checkErr) {
		labels["dnsResolve"] = "failure"
		return labels
	}
	if latency.DNS != 0 {
		labels["dnsResolve"] = "success"
	}
	if checkErr != nil {
		labels["tcpConnect"] = "failure"
		return labels
	}
	labels["tcpConnect"] = "success"
	return labels
}
