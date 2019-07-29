package configmetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisters "github.com/openshift/client-go/config/listers/config/v1"
)

// Register exposes core platform metrics that relate to the configuration
// of Kubernetes.
// TODO: in the future this may move to cluster-config-operator.
func Register(configInformer configinformers.SharedInformerFactory) {
	prometheus.MustRegister(&configMetrics{
		infrastructureLister: configInformer.Config().V1().Infrastructures().Lister(),
		cloudProvider: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_infrastructure_provider",
			Help: "Reports whether the cluster is configured with an infrastructure provider. type is unset if no cloud provider is recognized or set to the constant used by the Infrastructure config. Region is set when the cluster clearly identifies a region within the provider.",
		}, []string{"type", "region"}),
	})
}

// configMetrics implements metrics gathering for this component.
type configMetrics struct {
	infrastructureLister configlisters.InfrastructureLister
	cloudProvider        *prometheus.GaugeVec
}

// Describe reports the metadata for metrics to the prometheus collector.
func (m *configMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.cloudProvider.WithLabelValues("", "").Desc()
}

// Collect calculates metrics from the cached config and reports them to the prometheus collector.
func (m *configMetrics) Collect(ch chan<- prometheus.Metric) {
	if infra, err := m.infrastructureLister.Get("cluster"); err == nil {
		if status := infra.Status.PlatformStatus; status != nil {
			switch {
			case status.AWS != nil:
				ch <- m.cloudProvider.WithLabelValues(string(status.Type), status.AWS.Region)
			default:
				ch <- m.cloudProvider.WithLabelValues(string(status.Type), "")
			}
		}
	}
}
