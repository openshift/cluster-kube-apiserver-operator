package targetconfigcontroller

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption/secrets"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

var (
	apiserverScheme = runtime.NewScheme()
	apiserverCodecs = serializer.NewCodecFactory(apiserverScheme)
)

func init() {
	utilruntime.Must(apiserverv1.AddToScheme(apiserverScheme))
}

// AddKMSPluginToPodSpec conditionally adds the KMS plugin sidecar, volume, and volume mounts to the specified pod spec.
// Volume mounts are always appended; volumes are deduplicated by name.
// Deprecated: this is a temporary solution to get KMS TP v1 out. We should come up with a different approach afterwards.
func AddKMSPluginToPodSpec(podSpec *corev1.PodSpec, featureGateAccessor featuregates.FeatureGateAccess, secretLister corev1listers.SecretLister) error {
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
		klog.Infof("kms is disabled: feature gate %s is disabled", features.FeatureGateKMSEncryption)
		return nil
	}

	encryptionConfig, err := secretLister.Secrets("openshift-config-managed").Get("encryption-config-openshift-kube-apiserver")
	if apierrors.IsNotFound(err) {
		klog.Infof("kms is disabled: secret openshift-config-managed/encryption-config not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("kms is disabled: failed to get encryption-config-openshift-kube-apiserver secret: %v", err)
		return nil
	}

	encryptionConfigBytes, ok := encryptionConfig.Data["encryption-config"]
	if !ok {
		klog.Infof("kms is disabled: failed to get encryption-config key in secret")
		return nil
	}

	gvk := apiserverv1.SchemeGroupVersion.WithKind("EncryptionConfiguration")
	obj, _, err := apiserverCodecs.UniversalDeserializer().Decode(encryptionConfigBytes, &gvk, nil)
	if err != nil {
		return fmt.Errorf("kms is disabled: failed to decode: %w", err)
	}

	// FIXME: only Vault KMS plugin is supported for now, so any KMS configuration implies Vault
	config, ok := obj.(*apiserverv1.EncryptionConfiguration)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}

	shouldSetupVault := slices.ContainsFunc(config.Resources, func(resource apiserverv1.ResourceConfiguration) bool {
		return slices.ContainsFunc(resource.Providers, func(provider apiserverv1.ProviderConfiguration) bool {
			return provider.KMS != nil
		})
	})

	if !shouldSetupVault {
		klog.Infof("kms is disabled: vault should not be set up")
		return nil
	}

	// TODO: only the first KMS provider is used for now
	var kmsConfig *apiserverv1.KMSConfiguration
	for _, resource := range config.Resources {
		for _, provider := range resource.Providers {
			if provider.KMS != nil {
				kmsConfig = provider.KMS
				break
			}
		}
		if kmsConfig != nil {
			break
		}
	}

	if kmsConfig == nil || kmsConfig.Name == "" {
		return fmt.Errorf("no KMS provider found in EncryptionConfiguration")
	}

	// Parse keyID from the UDS endpoint, e.g. "unix:///var/run/kmsplugin/kms-555.sock" → "555"
	socketPath := strings.TrimPrefix(kmsConfig.Endpoint, "unix://")
	baseName := path.Base(socketPath)
	if !strings.HasPrefix(baseName, "kms-") || !strings.HasSuffix(baseName, ".sock") {
		return fmt.Errorf("unexpected KMS endpoint format: %s", kmsConfig.Endpoint)
	}
	keyID := strings.TrimSuffix(strings.TrimPrefix(baseName, "kms-"), ".sock")
	if keyID == "" {
		return fmt.Errorf("unexpected KMS endpoint format: %s", kmsConfig.Endpoint)
	}

	// Read the provider config from the encryption-config secret
	providerConfigKey := fmt.Sprintf("%s-%s", secrets.EncryptionSecretKMSProviderConfig, keyID)
	providerConfigData, ok := encryptionConfig.Data[providerConfigKey]
	if !ok {
		return fmt.Errorf("missing provider config key %s in encryption-config secret", providerConfigKey)
	}

	var kmsProviderConfig state.KMSProviderConfig
	if err := json.Unmarshal(providerConfigData, &kmsProviderConfig); err != nil {
		return fmt.Errorf("failed to unmarshal KMS provider config: %w", err)
	}

	if kmsProviderConfig.Vault == nil {
		return fmt.Errorf("only Vault KMS provider is supported")
	}

	// FIXME: credentials will be available in the encryptionConfiguration, but they are not there yet.
	// Get them from a custom secret for now
	credentials, err := secretLister.Secrets("openshift-config").Get("vault-kms-credentials")
	if apierrors.IsNotFound(err) {
		klog.Infof("kms is disabled: secret openshift-config/vault-kms-credentials not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("kms is disabled: failed to get vault-kms-credentials secret: %v", err)
		return nil
	}

	// FIXME: I want to use the real Vault KMS plugin instead of the mock one that is temporarily hardcoded in library-go
	kmsProviderConfig.Vault.Image = "quay.io/bertinatto/vault:v2"
	kmsProviderConfig.Vault.TransitMount = "transit"
	kmsProviderConfig.Vault.TransitKey = string(credentials.Data["VAULT_KEY_NAME"])
	kmsProviderConfig.Vault.VaultAddress = string(credentials.Data["VAULT_ADDR"])
	kmsProviderConfig.Vault.VaultNamespace = string(credentials.Data["VAULT_NAMESPACE"])

	klog.Infof("kms is enabled: found config, now patching kube-apiserver pod")
	if err := addKMSPluginSidecarToPodSpec(podSpec, "kms-plugin", kmsProviderConfig.Vault, kmsConfig, credentials); err != nil {
		return err
	}

	if err := addKMSPluginVolumeAndMountToPodSpec(podSpec, "kms-plugin"); err != nil {
		return err
	}

	if err := addKMSPluginVolumeAndMountToPodSpec(podSpec, "kube-apiserver"); err != nil {
		return err
	}

	return nil
}

func addKMSPluginSidecarToPodSpec(podSpec *corev1.PodSpec, containerName string, vaultConfig *state.VaultProviderConfig, kmsConfig *apiserverv1.KMSConfiguration, credentials *corev1.Secret) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	// FIXME: note that the secret is exposed here. This is temporary until the secret is stored in a encryption-config key
	args := fmt.Sprintf(`
	echo "%s" > /tmp/secret-id
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=%s \
	-transit-key=%s \
	-log-level=debug-extended \
	-approle-role-id=%s \
	-approle-secret-id-path=/tmp/secret-id`,
		credentials.Data["VAULT_SECRET_ID"],
		kmsConfig.Endpoint,
		vaultConfig.VaultAddress,
		vaultConfig.VaultNamespace,
		vaultConfig.TransitMount,
		vaultConfig.TransitKey,
		credentials.Data["VAULT_ROLE_ID"],
	)

	// TODO: set resource requests/limits for the KMS plugin sidecar
	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:  containerName,
		Image: vaultConfig.Image,
		// FIXME: This is temporary until the secret is stored in a encryption-config key. After that, we'll use the default entrypoint
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{args},
		// TODO: uncomment once the secret is stored in a encryption-config key
		// Args: []string{
		// 	"--log-level=debug-extended", // TODO: make log level configurable
		// 	fmt.Sprintf("--listen-address=%s", endpoint),
		// 	fmt.Sprintf("--vault-address=%s", vaultConfig.VaultAddress),
		// 	fmt.Sprintf("--vault-namespace=%s", vaultConfig.VaultNamespace),
		// 	fmt.Sprintf("--transit-key=%s", vaultConfig.TransitKey),
		// 	fmt.Sprintf("--transit-mount=%s", vaultConfig.TransitMount),
		// 	fmt.Sprintf("--approle-role-id=%s", string(credentials.Data["VAULT_ROLE_ID"])),
		// 	fmt.Sprintf("--approle-secret-id-path=/etc/kubernetes/static-pod-resources/%s", credentialsKey),
		// },
		// TODO: uncomment once the secret is stored in a encryption-config key
		// VolumeMounts: []corev1.VolumeMount{
		// 	{
		// 		Name:      "resource-dir",
		// 		MountPath: "/etc/kubernetes/static-pod-resources",
		// 	},
		// },
		SecurityContext: &corev1.SecurityContext{
			Privileged: new(bool),
		},
	})

	return nil
}

func addKMSPluginVolumeAndMountToPodSpec(podSpec *corev1.PodSpec, containerName string) error {
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
	container.VolumeMounts = append(container.VolumeMounts,
		corev1.VolumeMount{
			Name:      "kms-plugin-socket",
			MountPath: "/var/run/kmsplugin",
		},
	)

	foundVolume := false
	for _, volume := range podSpec.Volumes {
		if volume.Name == "kms-plugin-socket" {
			foundVolume = true
			break
		}
	}

	if !foundVolume {
		directoryOrCreate := corev1.HostPathDirectoryOrCreate
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name: "kms-plugin-socket",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/run/kmsplugin",
						Type: &directoryOrCreate,
					},
				},
			},
		)
	}

	return nil
}
