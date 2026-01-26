package kms

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
)

// AddKMSPluginVolumeToPod conditionally adds the KMS plugin volume mount to the kube-apiserver container.
// FIXME: this is a temporary solution to get KMS TP v1 out. We should come up with a different approach afterwards.
func AddKMSPluginVolumeToPod(pod *corev1.Pod, featureGateAccessor featuregates.FeatureGateAccess) error {
	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil
	}

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("failed to get feature gates: %w", err)
	}

	if !featureGates.Enabled(features.FeatureGateKMSEncryptionProvider) {
		return nil
	}

	directoryOrCreate := corev1.HostPathDirectoryOrCreate
	pod.Spec.Volumes = append(pod.Spec.Volumes,
		corev1.Volume{
			Name: "kms-socket",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/var/run/kmsplugin",
					Type: &directoryOrCreate,
				},
			},
		},
	)

	for i, container := range pod.Spec.Containers {
		if container.Name != "kube-apiserver" {
			continue
		}
		pod.Spec.Containers[i].VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				Name:      "kms-socket",
				MountPath: "/var/run/kmsplugin",
			},
		)
	}

	return nil
}
