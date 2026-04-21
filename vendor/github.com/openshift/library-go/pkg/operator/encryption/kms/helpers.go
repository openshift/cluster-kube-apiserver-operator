package kms

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/openshift/library-go/pkg/operator/encryption/secrets"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

var (
	apiserverScheme = runtime.NewScheme()
	apiserverCodecs = serializer.NewCodecFactory(apiserverScheme)
)

func init() {
	utilruntime.Must(apiserverv1.AddToScheme(apiserverScheme))
}

var kmsEndpointRegexp = regexp.MustCompile(`^unix:///var/run/kmsplugin/kms-(\d+)\.sock$`)

// AddKMSPluginVolumeAndMountToPodSpec adds an emptyDir volume for the KMS plugin socket and mounts it into the specified container.
func AddKMSPluginVolumeAndMountToPodSpec(podSpec *corev1.PodSpec, containerName string) error {
	containerIndex := -1
	for i, container := range podSpec.Containers {
		if container.Name == containerName {
			containerIndex = i
			break
		}
	}

	if containerIndex < 0 {
		return fmt.Errorf("container %s not found", containerName)
	}

	container := &podSpec.Containers[containerIndex]
	foundMount := false
	for _, m := range container.VolumeMounts {
		if m.Name == "kms-plugin-socket" {
			foundMount = true
			break
		}
	}
	if !foundMount {
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				Name:      "kms-plugin-socket",
				MountPath: "/var/run/kmsplugin",
			},
		)
	}

	foundVolumeInContainer := false
	for _, volume := range podSpec.Volumes {
		if volume.Name == "kms-plugin-socket" {
			foundVolumeInContainer = true
			break
		}
	}

	if !foundVolumeInContainer {
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name: "kms-plugin-socket",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	}

	return nil
}

func ParseKeyIDFromEndpoint(endpoint string) (string, error) {
	matches := kmsEndpointRegexp.FindStringSubmatch(endpoint)
	if matches == nil {
		return "", fmt.Errorf("unexpected KMS endpoint format: %s", endpoint)
	}
	return matches[1], nil
}

func DecodeEncryptionConfiguration(data []byte) (*apiserverv1.EncryptionConfiguration, error) {
	gvk := apiserverv1.SchemeGroupVersion.WithKind("EncryptionConfiguration")
	obj, _, err := apiserverCodecs.UniversalDeserializer().Decode(data, &gvk, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EncryptionConfiguration: %w", err)
	}
	config, ok := obj.(*apiserverv1.EncryptionConfiguration)
	if !ok {
		return nil, fmt.Errorf("unexpected type: %T", obj)
	}
	return config, nil
}

func GetKMSProviderConfig(secretData map[string][]byte, keyID string) (*state.KMSProviderConfig, error) {
	providerConfigKey := fmt.Sprintf("%s-%s", secrets.EncryptionSecretKMSProviderConfig, keyID)
	providerConfigData, ok := secretData[providerConfigKey]
	if !ok {
		return nil, fmt.Errorf("missing provider config key %s in encryption-config secret", providerConfigKey)
	}
	var kmsProviderConfig state.KMSProviderConfig
	if err := json.Unmarshal(providerConfigData, &kmsProviderConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal KMS provider config: %w", err)
	}
	return &kmsProviderConfig, nil
}
