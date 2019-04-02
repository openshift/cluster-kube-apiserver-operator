package certmonitor

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/util/cert"

	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/openshift/library-go/pkg/crypto"
)

const (
	CertificateMonitoredLabelName = "auth.openshift.io/certificate-monitored"
	CertificateTypeAnnotationName = "auth.openshift.io/certificate-type"
)

type CertificateType string

var (
	CertificateTypeCABundle CertificateType = "ca-bundle"
	CertificateTypeSigner   CertificateType = "signer"
	CertificateTypeTarget   CertificateType = "target"
)

var timeNowFn = time.Now

var (
	caBundleExpireHoursDesc = prometheus.NewDesc(
		"certificates_ca_bundle_expire_hours",
		"Number of hours until certificates in given CA bundle expire",
		[]string{"namespace", "name", "common_name", "signer_name", "valid_from"}, nil)

	signerExpireHoursDesc = prometheus.NewDesc(
		"certificates_signer_expire_hours",
		"Number of hours until certificates in given signer expire",
		[]string{"namespace", "name", "common_name", "signer_name", "valid_from"}, nil)

	targetExpireHoursDesc = prometheus.NewDesc(
		"certificates_target_expire_hours",
		"Number of hours until certificates in given target expire",
		[]string{"namespace", "name", "common_name", "signer_name", "valid_from"}, nil)
)

type certMetricsCollector struct {
	configLister corev1listers.ConfigMapLister
	secretLister corev1listers.SecretLister

	nowFn func() time.Time
}

func (c *certMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- caBundleExpireHoursDesc
	ch <- signerExpireHoursDesc
	ch <- targetExpireHoursDesc
}

func (c *certMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.collectCABundles(ch)
	c.collectSignersAndTarget(ch)
}

func (c *certMetricsCollector) collectSignersAndTarget(ch chan<- prometheus.Metric) {
	secrets, err := c.secretLister.List(labels.SelectorFromSet(map[string]string{CertificateMonitoredLabelName: "true"}))
	if err != nil {
		glog.Warningf("failed to list signer secrets: %v", err)
		return
	}

	for _, secret := range secrets {
		if secret.Data["tls.crt"] == nil || secret.Data["tls.key"] == nil {
			glog.V(4).Infof("secret %s/%s does not have 'tls.crt' or 'tls.key'", secret.Namespace, secret.Name)
			continue
		}

		signingCertKeyPair, err := crypto.GetCAFromBytes(secret.Data["tls.crt"], secret.Data["tls.key"])
		if err != nil {
			continue
		}

		if secret.Annotations == nil {
			continue
		}

		var targetDescType *prometheus.Desc
		switch CertificateType(secret.Annotations[CertificateTypeAnnotationName]) {
		case CertificateTypeSigner:
			targetDescType = signerExpireHoursDesc
		case CertificateTypeTarget:
			targetDescType = targetExpireHoursDesc
		default:
			glog.Warningf("secret %s/%s has unknown certificate type: %q", secret.Namespace, secret.Name, secret.Annotations[CertificateTypeAnnotationName])
			continue
		}

		labelValues := []string{}
		for _, certificate := range signingCertKeyPair.Config.Certs {
			expireHours := certificate.NotAfter.UTC().Sub(c.nowFn().UTC()).Hours()
			labelValues = append(labelValues, []string{
				secret.Namespace,
				secret.Name,
				certificate.Subject.CommonName,
				certificate.Issuer.CommonName,
				fmt.Sprintf("%s", certificate.NotBefore.UTC()),
			}...)

			ch <- prometheus.MustNewConstMetric(
				targetDescType,
				prometheus.GaugeValue,
				float64(expireHours),
				labelValues...)
		}
	}
}

func (c *certMetricsCollector) collectCABundles(ch chan<- prometheus.Metric) {
	configs, err := c.configLister.List(labels.SelectorFromSet(map[string]string{CertificateMonitoredLabelName: "true"}))
	if err != nil {
		glog.Warningf("failed to list ca bundle configmaps: %v", err)
		return
	}

	for _, config := range configs {
		if _, exists := config.Data["ca-bundle.crt"]; !exists {
			glog.V(4).Infof("configmap %s/%s does not have 'ca-bundle.crt'", config.Namespace, config.Name)
			continue
		}
		certificates, err := cert.ParseCertsPEM([]byte(config.Data["ca-bundle.crt"]))
		if err != nil {
			glog.V(2).Infof("configmap %s/%s 'ca-bundle.crt' has invalid certificates: %v", config.Namespace, config.Name, err)
			continue
		}

		labelValues := []string{}
		for _, certificate := range certificates {
			expireHours := certificate.NotAfter.UTC().Sub(c.nowFn().UTC()).Hours()
			labelValues = append(labelValues, []string{
				config.Namespace,
				config.Name,
				certificate.Subject.CommonName,
				certificate.Issuer.CommonName,
				fmt.Sprintf("%s", certificate.NotBefore.UTC()),
			}...)

			ch <- prometheus.MustNewConstMetric(
				caBundleExpireHoursDesc,
				prometheus.GaugeValue,
				float64(expireHours),
				labelValues...)
		}
	}
}

// registered avoids double prometheus registration
var registered bool

func Register(configMaps corev1listers.ConfigMapLister, secrets corev1listers.SecretLister) {
	if registered {
		return
	}
	defer func() {
		registered = true
	}()
	collector := &certMetricsCollector{
		configLister: configMaps,
		secretLister: secrets,
		nowFn:        timeNowFn,
	}
	prometheus.MustRegister(collector)
	glog.Infof("Registered certificate monitoring Prometheus metrics")
}
