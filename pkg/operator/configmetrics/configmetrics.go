package configmetrics

import (
	"github.com/blang/semver"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/component-base/metrics/legacyregistry"

	configv1 "github.com/openshift/api/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisters "github.com/openshift/client-go/config/listers/config/v1"
)

// Register exposes core platform metrics that relate to the configuration
// of Kubernetes.
// TODO: in the future this may move to cluster-config-operator.
func Register(configInformer configinformers.SharedInformerFactory) {
	legacyregistry.MustRegister(&configMetrics{
		infrastructureLister: configInformer.Config().V1().Infrastructures().Lister(),
		cloudProvider: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_infrastructure_provider",
			Help: "Reports whether the cluster is configured with an infrastructure provider. type is unset if no cloud provider is recognized or set to the constant used by the Infrastructure config. region is set when the cluster clearly identifies a region within the provider. The value is 1 if a cloud provider is set or 0 if it is unset.",
		}, []string{"type", "region"}),
		featuregateLister: configInformer.Config().V1().FeatureGates().Lister(),
		featureSet: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_feature_set",
			Help: "Reports the feature set the cluster is configured to expose. name corresponds to the featureSet field of the cluster. The value is 1 if a cloud provider is supported.",
		}, []string{"name"}),
	})
}

// configMetrics implements metrics gathering for this component.
type configMetrics struct {
	cloudProvider        *prometheus.GaugeVec
	featureSet           *prometheus.GaugeVec
	infrastructureLister configlisters.InfrastructureLister
	featuregateLister    configlisters.FeatureGateLister
}

func (m *configMetrics) Create(version *semver.Version) bool {
	return true
}

// Describe reports the metadata for metrics to the prometheus collector.
func (m *configMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.cloudProvider.WithLabelValues("", "").Desc()
	ch <- m.featureSet.WithLabelValues("").Desc()
}

// Collect calculates metrics from the cached config and reports them to the prometheus collector.
func (m *configMetrics) Collect(ch chan<- prometheus.Metric) {
	if infra, err := m.infrastructureLister.Get("cluster"); err == nil {
		if status := infra.Status.PlatformStatus; status != nil {
			var g prometheus.Gauge
			var value float64 = 1
			switch {
			// it is illegal to set type to empty string, so let the default case handle
			// empty string (so we can detect it) while preserving the constant None here
			case status.Type == configv1.NonePlatformType:
				g = m.cloudProvider.WithLabelValues(string(status.Type), "")
				value = 0
			case status.AWS != nil:
				g = m.cloudProvider.WithLabelValues(string(status.Type), status.AWS.Region)
			case status.GCP != nil:
				g = m.cloudProvider.WithLabelValues(string(status.Type), status.GCP.Region)
			default:
				g = m.cloudProvider.WithLabelValues(string(status.Type), "")
			}
			g.Set(value)
			ch <- g
		}
	}
	if features, err := m.featuregateLister.Get("cluster"); err == nil {
		g := m.featureSet.WithLabelValues(string(features.Spec.FeatureSet))
		if features.Spec.FeatureSet == configv1.Default {
			g.Set(1)
		} else {
			g.Set(0)
		}
		ch <- g
	}
}

func (m *configMetrics) ClearState() {}
