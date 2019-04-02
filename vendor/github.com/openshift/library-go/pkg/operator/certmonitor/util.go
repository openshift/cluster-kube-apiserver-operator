package certmonitor

import (
	"k8s.io/api/core/v1"
)

func SetCertificateMonitoredConfigMap(config *v1.ConfigMap, certificateType CertificateType) {
	if config.Labels == nil {
		config.Labels = map[string]string{}
	}
	config.Labels[CertificateMonitoredLabelName] = "true"

	if config.Annotations == nil {
		config.Annotations = map[string]string{}
	}
	config.Annotations[CertificateTypeAnnotationName] = string(certificateType)
}

func SetCertificateMonitoredSecret(secret *v1.Secret, certificateType CertificateType) {
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[CertificateMonitoredLabelName] = "true"

	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[CertificateTypeAnnotationName] = string(certificateType)
}
