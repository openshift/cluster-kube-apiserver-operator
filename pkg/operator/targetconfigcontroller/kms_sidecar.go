package targetconfigcontroller

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption/secrets"
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
func AddKMSPluginToPodSpec(podSpec *corev1.PodSpec, featureGateAccessor featuregates.FeatureGateAccess, secretLister corev1listers.SecretLister, kmsPluginImage string) error {
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

	// KMS provider name format is "{keyID}_{resource}", e.g. "1_secrets"
	parts := strings.SplitN(kmsConfig.Name, "_", 2)
	if len(parts) != 2 {
		return fmt.Errorf("unexpected KMS provider name format: %s", kmsConfig.Name)
	}
	keyID := parts[0]

	// Read the provider config (configv1.KMSConfig) from the encryption-config secret
	providerConfigKey := fmt.Sprintf("%s-%s", secrets.EncryptionSecretKMSProviderConfig, keyID)
	providerConfigData, ok := encryptionConfig.Data[providerConfigKey]
	if !ok {
		return fmt.Errorf("missing provider config key %s in encryption-config secret", providerConfigKey)
	}

	var kmsProviderConfig configv1.KMSConfig
	if err := json.Unmarshal(providerConfigData, &kmsProviderConfig); err != nil {
		return fmt.Errorf("failed to unmarshal KMS provider config: %w", err)
	}

	if kmsProviderConfig.Type != configv1.VaultKMSProvider || kmsProviderConfig.Vault == nil {
		return fmt.Errorf("only Vault KMS provider is supported, got type %q", kmsProviderConfig.Type)
	}

	// Read the credentials (map[string][]byte with "roleID" and "secretID" keys)
	credentialsKey := fmt.Sprintf("%s-%s", secrets.EncryptionSecretKMSSecretData, keyID)
	credentialsData, ok := encryptionConfig.Data[credentialsKey]
	if !ok {
		return fmt.Errorf("missing credentials key %s in encryption-config secret", credentialsKey)
	}

	var credentials map[string][]byte
	if err := json.Unmarshal(credentialsData, &credentials); err != nil {
		return fmt.Errorf("failed to unmarshal KMS credentials: %w", err)
	}

	klog.Infof("kms is enabled: found config, now patching kube-apiserver pod")
	if err := addKMSPluginSidecarToPodSpec(podSpec, "kms-plugin", kmsPluginImage, kmsProviderConfig.Vault, kmsConfig.Endpoint, credentials, credentialsKey); err != nil {
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

func addKMSPluginSidecarToPodSpec(podSpec *corev1.PodSpec, containerName, image string, vaultConfig *configv1.VaultKMSConfig, endpoint string, credentials map[string][]byte, credentialsKey string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	// TODO: set resource requests/limits for the KMS plugin sidecar
	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:  containerName,
		Image: image,
		Args: []string{
			"--log-level=debug-extended", // TODO: make log level configurable
			fmt.Sprintf("--listen-address=%s", endpoint),
			fmt.Sprintf("--vault-address=%s", vaultConfig.VaultAddress),
			fmt.Sprintf("--vault-namespace=%s", vaultConfig.VaultNamespace),
			fmt.Sprintf("--transit-key=%s", vaultConfig.TransitKey),
			fmt.Sprintf("--transit-mount=%s", vaultConfig.TransitMount),
			fmt.Sprintf("--approle-role-id=%s", string(credentials["roleID"])),
			fmt.Sprintf("--approle-secret-id-path=/etc/kubernetes/static-pod-resources/%s", credentialsKey),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "resource-dir",
				MountPath: "/etc/kubernetes/static-pod-resources",
			},
		},
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
