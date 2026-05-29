package pluginlifecycle

import (
	"context"
	"fmt"
	"path/filepath"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption/encryptiondata"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	resourceDirVolumeName = "resource-dir"
	credentialsVolumeName = "kms-plugin-credentials"
	credentialsMountPath  = "/var/run/secrets/kms-plugin"
)

// sidecarProvider abstracts the construction of a KMS plugin sidecar container for a specific provider (e.g. Vault).
type sidecarProvider interface {
	// Name returns the identifier used to name the sidecar container and locate its volume mounts.
	Name() string
	// BuildSidecarContainer returns a fully configured sidecar container ready to be injected into the API server pod
	BuildSidecarContainer() (corev1.Container, error)
}

// newSidecarProvider creates a provider-specific SidecarProvider for the given keyID, UDS endpoint, and plugin configuration.
func newSidecarProvider(keyID string, udsPath string, pluginConfig configv1.KMSPluginConfig, secretData state.KMSSecretData, credentialsDir string) (sidecarProvider, error) {
	switch pluginConfig.Type {
	case configv1.VaultKMSProvider:
		return newVaultSidecarProvider("vault-kms-plugin", keyID, udsPath, pluginConfig, secretData, credentialsDir)
	default:
		return nil, fmt.Errorf("unsupported KMS plugin configuration")
	}
}

// AddKMSPluginSidecarToStaticPodSpec discovers KMS plugins from the encryption-config secret and injects a sidecar
// container for each one into a kube-apiserver static pod spec. Credentials are accessed via the resource-dir volume
// mount that the static pod revision controller populates on disk.
// It is a no-op when the KMSEncryption feature gate is not enabled or the encryption-config secret does not exist.
func AddKMSPluginSidecarToStaticPodSpec(ctx context.Context, podSpec *corev1.PodSpec, containerName string, encryptionConfigNamespace string, encryptionConfigSecretName string, resourcesDir string, secretClient corev1client.SecretsGetter, featureGateAccessor featuregates.FeatureGateAccess) error {
	credentialsDir := filepath.Join(resourcesDir, "secrets", encryptionConfigSecretName)

	sidecarNames, err := addKMSPluginSidecars(ctx, podSpec, containerName, encryptionConfigNamespace, encryptionConfigSecretName, secretClient, featureGateAccessor, credentialsDir)
	if err != nil {
		return err
	}

	for _, name := range sidecarNames {
		if err := ensureVolumeMountInContainer(podSpec.InitContainers, name, resourceDirVolumeName, resourcesDir, true); err != nil {
			return err
		}
		setRunAsUser(podSpec.InitContainers, name, ptr.To(int64(0)))
	}

	return nil
}

// AddKMSPluginSidecarToPodSpec discovers KMS plugins from the encryption-config secret and injects a sidecar
// container for each one into an aggregated API server pod spec. The encryption-config secret is mounted as a
// volume to provide credentials.
// It is a no-op when the KMSEncryption feature gate is not enabled or the encryption-config secret does not exist.
func AddKMSPluginSidecarToPodSpec(ctx context.Context, podSpec *corev1.PodSpec, containerName string, encryptionConfigNamespace string, encryptionConfigSecretName string, secretClient corev1client.SecretsGetter, featureGateAccessor featuregates.FeatureGateAccess) error {
	sidecarNames, err := addKMSPluginSidecars(ctx, podSpec, containerName, encryptionConfigNamespace, encryptionConfigSecretName, secretClient, featureGateAccessor, credentialsMountPath)
	if err != nil {
		return err
	}

	if len(sidecarNames) == 0 {
		return nil
	}

	for _, name := range sidecarNames {
		if err := ensureVolumeMountInContainer(podSpec.InitContainers, name, credentialsVolumeName, credentialsMountPath, true); err != nil {
			return err
		}
	}

	ensureCredentialsVolume(podSpec, encryptionConfigSecretName)

	return nil
}

// addKMSPluginSidecars contains the shared logic for discovering KMS plugins and injecting sidecar containers.
// It returns the names of the sidecar containers that were injected, so callers can add deployment-mode-specific
// volume mounts.
func addKMSPluginSidecars(ctx context.Context, podSpec *corev1.PodSpec, containerName string, encryptionConfigNamespace string, encryptionConfigSecretName string, secretClient corev1client.SecretsGetter, featureGateAccessor featuregates.FeatureGateAccess, credentialsDir string) ([]string, error) {
	if podSpec == nil {
		return nil, fmt.Errorf("pod spec cannot be nil")
	}

	if containerName == "" {
		return nil, fmt.Errorf("container name cannot be empty")
	}

	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil, nil
	}

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return nil, fmt.Errorf("failed to get feature gates: %w", err)
	}

	if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
		return nil, nil
	}

	encryptionConfigurationSecret, err := secretClient.Secrets(encryptionConfigNamespace).Get(ctx, encryptionConfigSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(4).Infof("skipping KMS sidecar injection: %s/%s secret not found", encryptionConfigNamespace, encryptionConfigSecretName)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s secret: %w", encryptionConfigNamespace, encryptionConfigSecretName, err)
	}

	encryptionConfig, err := encryptiondata.FromSecret(encryptionConfigurationSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to extract encryption config from %s/%s secret: %w", encryptionConfigNamespace, encryptionConfigSecretName, err)
	}

	kmsConfigurations, err := encryptiondata.ExtractUniqueAndSortedKMSConfigurations(encryptionConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get KMS configurations: %w", err)
	}
	if len(kmsConfigurations) == 0 {
		klog.V(4).Infof("skipping KMS sidecar injection: no KMS plugins found in EncryptionConfiguration")
		return nil, nil
	}

	klog.V(4).Infof("injecting %d KMS sidecar(s)", len(kmsConfigurations))

	var sidecarNames []string
	for _, kmsConfiguration := range kmsConfigurations {
		// ExtractUniqueAndSortedKMSConfigurations function rewrites the .Name field to include only the key ID
		keyID := kmsConfiguration.Name
		udsPath := kmsConfiguration.Endpoint

		pluginConfig, ok := encryptionConfig.KMSPlugins[keyID]
		if !ok {
			return nil, fmt.Errorf("missing plugin config for keyID %s", keyID)
		}

		var secretData state.KMSSecretData
		if encryptionConfig.KMSPluginsSecretData.ByKeyID != nil {
			secretData = encryptionConfig.KMSPluginsSecretData.ByKeyID[keyID]
		}

		provider, err := newSidecarProvider(keyID, udsPath, pluginConfig, secretData, credentialsDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create a sidecar provider for keyID %s: %w", keyID, err)
		}

		if err := ensureSidecarContainer(podSpec, provider); err != nil {
			return nil, err
		}

		if err := ensureVolumeMountInContainer(podSpec.InitContainers, provider.Name(), "kms-plugin-socket", "/var/run/kmsplugin", false); err != nil {
			return nil, err
		}

		sidecarNames = append(sidecarNames, provider.Name())
	}

	if err := ensureVolumeMountInContainer(podSpec.Containers, containerName, "kms-plugin-socket", "/var/run/kmsplugin", false); err != nil {
		return nil, err
	}

	// The volume mount in the kube-apiserver and KMS plugin containers requires a volume in the podSpec
	ensureSocketVolume(podSpec)

	return sidecarNames, nil
}

func ensureSidecarContainer(podSpec *corev1.PodSpec, provider sidecarProvider) error {
	sidecar, err := provider.BuildSidecarContainer()
	if err != nil {
		return fmt.Errorf("failed to build sidecar container: %w", err)
	}

	for i, container := range podSpec.InitContainers {
		if container.Name == sidecar.Name {
			podSpec.InitContainers[i] = sidecar
			return nil
		}
	}

	podSpec.InitContainers = append(podSpec.InitContainers, sidecar)
	return nil
}

func ensureVolumeMountInContainer(containers []corev1.Container, containerName, volumeName, mountPath string, readOnly bool) error {
	containerIndex := -1
	for i, container := range containers {
		if container.Name == containerName {
			containerIndex = i
			break
		}
	}

	if containerIndex < 0 {
		return fmt.Errorf("container %s not found", containerName)
	}

	container := &containers[containerIndex]
	for _, m := range container.VolumeMounts {
		if m.Name == volumeName {
			return nil
		}
	}
	container.VolumeMounts = append(container.VolumeMounts,
		corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
			ReadOnly:  readOnly,
		},
	)
	return nil
}

func ensureSocketVolume(podSpec *corev1.PodSpec) {
	for _, volume := range podSpec.Volumes {
		if volume.Name == "kms-plugin-socket" {
			return
		}
	}

	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{
			Name: "kms-plugin-socket",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	)
}

func setRunAsUser(containers []corev1.Container, containerName string, uid *int64) {
	for i, c := range containers {
		if c.Name == containerName {
			if c.SecurityContext == nil {
				containers[i].SecurityContext = &corev1.SecurityContext{}
			}
			containers[i].SecurityContext.RunAsUser = uid
			return
		}
	}
}

func ensureCredentialsVolume(podSpec *corev1.PodSpec, secretName string) {
	for _, volume := range podSpec.Volumes {
		if volume.Name == credentialsVolumeName {
			return
		}
	}

	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{
			Name: credentialsVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		},
	)
}
