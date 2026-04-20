package targetconfigcontroller

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	encryptionkms "github.com/openshift/library-go/pkg/operator/encryption/kms"
	"github.com/openshift/library-go/pkg/operator/encryption/kms/plugins"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

const (
	kmsPluginSocketVolumeName = "kms-plugin-socket"
	kmsPluginSocketMountPath  = "/var/run/kmsplugin"
)

// AddKMSPluginToPodSpec conditionally adds the KMS plugin sidecar, volume, and volume mounts to the specified pod spec.
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
		klog.Infof("KMS is disabled: feature gate %s is disabled", features.FeatureGateKMSEncryption)
		return nil
	}

	encryptionConfig, err := secretLister.Secrets("openshift-config-managed").Get("encryption-config-openshift-kube-apiserver")
	if err != nil {
		klog.Infof("KMS is disabled: failed to get encryption-config-openshift-kube-apiserver secret: %v", err)
		return nil
	}

	encryptionConfigBytes, ok := encryptionConfig.Data["encryption-config"]
	if !ok {
		klog.Infof("KMS is disabled: failed to get encryption-config key in secret")
		return nil
	}

	config, err := encryptionkms.DecodeEncryptionConfiguration(encryptionConfigBytes)
	if err != nil {
		return fmt.Errorf("KMS is disabled: %w", err)
	}

	kmsConfig := findFirstKMSProvider(config)
	if kmsConfig == nil {
		klog.Infof("KMS is disabled: no KMS provider found in EncryptionConfiguration")
		return nil
	}

	keyID, err := encryptionkms.ParseKeyIDFromEndpoint(kmsConfig.Endpoint)
	if err != nil {
		return err
	}

	providerConfig, err := encryptionkms.GetKMSProviderConfig(encryptionConfig.Data, keyID)
	if err != nil {
		return err
	}

	if providerConfig.Vault == nil {
		return fmt.Errorf("only Vault KMS provider is supported")
	}

	// FIXME: credentials will be available in the encryptionConfiguration, but they are not there yet.
	credentials, err := secretLister.Secrets("openshift-config").Get("vault-kms-credentials")
	if apierrors.IsNotFound(err) {
		klog.Infof("KMS is disabled: secret openshift-config/vault-kms-credentials not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("KMS is disabled: failed to get vault-kms-credentials secret: %v", err)
		return nil
	}

	klog.Infof("KMS is enabled: found config, now patching kube-apiserver pod")

	provider := &plugins.VaultSidecarProvider{
		Config:      providerConfig.Vault,
		Credentials: credentials,
	}

	if err := encryptionkms.AddSidecarContainer(podSpec, provider, "kms-plugin", kmsConfig); err != nil {
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
			Name:      kmsPluginSocketVolumeName,
			MountPath: kmsPluginSocketMountPath,
		},
	)

	foundVolume := false
	for _, volume := range podSpec.Volumes {
		if volume.Name == kmsPluginSocketVolumeName {
			foundVolume = true
			break
		}
	}
	if !foundVolume {
		directoryOrCreate := corev1.HostPathDirectoryOrCreate
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name: kmsPluginSocketVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: kmsPluginSocketMountPath,
						Type: &directoryOrCreate,
					},
				},
			},
		)
	}

	return nil
}

func findFirstKMSProvider(config *apiserverv1.EncryptionConfiguration) *apiserverv1.KMSConfiguration {
	for _, resource := range config.Resources {
		for _, provider := range resource.Providers {
			if provider.KMS != nil {
				return provider.KMS
			}
		}
	}
	return nil
}
