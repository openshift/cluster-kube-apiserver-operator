package targetconfigcontroller

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/encryption/encryptionconfig"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/klog/v2"
)

const (
	// TODO: this will be replaced by the kms-provider-config data field from the encryption-key secret
	// once the library-go encryption controllers support it.
	kmsPluginImage      = "quay.io/rhn_support_rgangwar/mock-kms-plugin-vault:latest"
	kmsSocketVolumeName = "kms-plugin-socket"
	kmsSocketMountPath  = "/var/run/kmsplugin"
)

// AddKMSPluginToPodSpecFn returns a PodMutationFunc that injects KMS plugin
// sidecar containers into the kube-apiserver static pod based on the
// revisioned encryption-config.
//
// The function reads the encryption-config-{revision} secret, parses it to
// find KMS providers, and for each unique KMS endpoint injects a sidecar
// container configured to listen on that endpoint.
func AddKMSPluginToPodSpecFn(o *installerpod.InstallOptions) installerpod.PodMutationFunc {
	return func(pod *corev1.Pod) error {
		secretName := fmt.Sprintf("%s-%s", encryptionconfig.EncryptionConfSecretName, o.Revision)
		secret, err := o.KubeClient.CoreV1().Secrets(o.Namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
		if err != nil {
			klog.V(4).Infof("No encryption config secret %s/%s: %v", o.Namespace, secretName, err)
			return nil
		}

		encConfig, err := encryptionconfig.FromSecret(secret)
		if err != nil {
			return fmt.Errorf("failed to parse encryption config from secret %s: %w", secretName, err)
		}
		if encConfig == nil {
			return nil
		}

		kmsEndpoints := extractUniqueKMSEndpoints(encConfig)
		if len(kmsEndpoints) == 0 {
			return nil
		}

		klog.Infof("Found %d unique KMS endpoint(s), injecting sidecar containers", len(kmsEndpoints))

		addKMSSocketVolume(&pod.Spec)

		if err := addKMSSocketVolumeMount(&pod.Spec, "kube-apiserver"); err != nil {
			return err
		}

		for keyID, endpoint := range kmsEndpoints {
			addKMSSidecarContainer(&pod.Spec, keyID, endpoint)
		}

		return nil
	}
}

// extractUniqueKMSEndpoints returns a map of keyID -> endpoint for all unique
// KMS providers in the encryption config. Multiple resources (e.g., secrets
// and configmaps) that share the same KMS endpoint are deduplicated.
func extractUniqueKMSEndpoints(config *apiserverconfigv1.EncryptionConfiguration) map[string]string {
	endpoints := make(map[string]string)
	seenEndpoints := make(map[string]bool)

	for _, rc := range config.Resources {
		for _, provider := range rc.Providers {
			if provider.KMS == nil {
				continue
			}
			endpoint := provider.KMS.Endpoint
			if seenEndpoints[endpoint] {
				continue
			}
			seenEndpoints[endpoint] = true

			keyID := extractKeyIDFromProviderName(provider.KMS.Name)
			endpoints[keyID] = endpoint
		}
	}
	return endpoints
}

// extractKeyIDFromProviderName extracts the key ID from a KMS provider name.
// Provider names have the format "{keyID}_{resource}", e.g. "1_secrets".
func extractKeyIDFromProviderName(name string) string {
	for i, c := range name {
		if c == '_' {
			return name[:i]
		}
	}
	return name
}

func addKMSSocketVolume(podSpec *corev1.PodSpec) {
	for _, v := range podSpec.Volumes {
		if v.Name == kmsSocketVolumeName {
			return
		}
	}

	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: kmsSocketVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
}

func addKMSSocketVolumeMount(podSpec *corev1.PodSpec, containerName string) error {
	for i, container := range podSpec.Containers {
		if container.Name == containerName {
			podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      kmsSocketVolumeName,
					MountPath: kmsSocketMountPath,
				},
			)
			return nil
		}
	}
	return fmt.Errorf("container %s not found", containerName)
}

func addKMSSidecarContainer(podSpec *corev1.PodSpec, keyID, endpoint string) {
	containerName := fmt.Sprintf("kms-plugin-%s", keyID)

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:    containerName,
		Image:   kmsPluginImage,
		Command: []string{"/mock-vault-kms"},
		Args:    []string{fmt.Sprintf("--listen-address=%s", endpoint)},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kmsSocketVolumeName,
				MountPath: kmsSocketMountPath,
			},
		},
	})
}
