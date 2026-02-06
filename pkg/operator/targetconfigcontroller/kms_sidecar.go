package targetconfigcontroller

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	kmsPluginContainerName = "kms-plugin"
	kmsPluginSocketVolume  = "kms-plugin-socket"
	kmsPluginSocketPath    = "/var/run/kmsplugin"
)

func addKMSPluginSidecar(podSpec *corev1.PodSpec, kmsPluginImage string, featureGateAccessor featuregates.FeatureGateAccess) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil
	}

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("failed to get feature gates: %w", err)
	}

	if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
		return nil
	}

	for _, container := range podSpec.Containers {
		if container.Name == kmsPluginContainerName {
			return nil
		}
	}

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            kmsPluginContainerName,
		Image:           kmsPluginImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/kms-plugin"},
		Args: []string{
			"--socket=" + kmsPluginSocketPath + "/kms.sock",
		},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kmsPluginSocketVolume,
				MountPath: kmsPluginSocketPath,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("5m"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: ptrBool(true),
		},
	})

	return nil
}

func ptrBool(b bool) *bool {
	return &b
}
