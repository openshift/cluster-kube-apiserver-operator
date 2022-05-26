package configmetrics

import (
	"github.com/blang/semver/v4"
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
		proxyLister: configInformer.Config().V1().Proxies().Lister(),
		proxyEnablement: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_proxy_enabled",
			Help: "Reports whether the cluster has been configured to use a proxy. type is which type of proxy configuration has been set - http for an http proxy, https for an https proxy, and trusted_ca if a custom CA was specified.",
		}, []string{"type"}),
	})
}

// configMetrics implements metrics gathering for this component.
type configMetrics struct {
	cloudProvider        *prometheus.GaugeVec
	featureSet           *prometheus.GaugeVec
	proxyEnablement      *prometheus.GaugeVec
	infrastructureLister configlisters.InfrastructureLister
	featuregateLister    configlisters.FeatureGateLister
	proxyLister          configlisters.ProxyLister
}

func (m *configMetrics) Create(version *semver.Version) bool {
	return true
}

// Describe reports the metadata for metrics to the prometheus collector.
func (m *configMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.cloudProvider.WithLabelValues("", "").Desc()
	ch <- m.featureSet.WithLabelValues("").Desc()
	ch <- m.proxyEnablement.WithLabelValues("").Desc()
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
		ch <- booleanGaugeValue(
			m.featureSet.WithLabelValues(string(features.Spec.FeatureSet)),
			features.Spec.FeatureSet == configv1.Default,
		)
	}
	if proxy, err := m.proxyLister.Get("cluster"); err == nil {
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("http"), len(proxy.Spec.HTTPProxy) > 0)
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("https"), len(proxy.Spec.HTTPSProxy) > 0)
		ch <- booleanGaugeValue(m.proxyEnablement.WithLabelValues("trusted_ca"), len(proxy.Spec.TrustedCA.Name) > 0)
	}
}

func booleanGaugeValue(g prometheus.Gauge, value bool) prometheus.Gauge {
	if value {
		g.Set(1)
	} else {
		g.Set(0)
	}
	return g
}

func (m *configMetrics) ClearState() {}

func (m *configMetrics) FQName() string {
	return "cluster_kube_apiserver_operator"
}
