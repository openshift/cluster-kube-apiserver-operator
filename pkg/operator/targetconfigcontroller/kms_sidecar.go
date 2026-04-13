package targetconfigcontroller

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
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
	kmsNameRegex    = regexp.MustCompile(`-(\d+)_`)
)

func init() {
	utilruntime.Must(apiserverv1.AddToScheme(apiserverScheme))
}

// AddKMSPluginToPodSpec conditionally adds the KMS plugin volume mount to the specified container.
// It assumes the pod spec does not already contain the KMS volume or mount; no deduplication is performed.
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

	// is kms specified?
	klog.Infof("kms is enabled: trying to find credentials")

	// FIXME: credentials will be available in the encryptionConfiguration, but they are not there yet.
	// Get them from a custom secret for now
	// creds, err := secretLister.Secrets("openshift-config").Get("vault-kms-credentials")
	// if apierrors.IsNotFound(err) {
	// 	klog.Infof("kms is disabled: secret openshift-config/vault-kms-credentials not found: %v", err)
	// 	return nil
	// }
	// if err != nil {
	// 	klog.Infof("kms is disabled: failed to get vault-kms-credentials secret: %v", err)
	// 	return nil
	// }

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

	// name format: kms-<ID>_<resource>, e.g. "kms-2_secrets"
	kmsName := kmsConfig.Name
	m := kmsNameRegex.FindStringSubmatch(kmsName)
	if m == nil {
		return fmt.Errorf("unexpected KMS provider name format: %s", kmsName)
	}
	keyID, err := strconv.Atoi(m[1])
	if err != nil {
		return fmt.Errorf("failed to parse key ID from KMS provider name %q: %w", kmsName, err)
	}

	keyKMSProviderConfig := fmt.Sprintf("kms-provider-config-%d", keyID)
	keySecretID := fmt.Sprintf("kms-secret-id-%d", keyID)
	endpoint := kmsConfig.Endpoint

	vaultConfig := &vaultConfiguration{}
	if err := json.Unmarshal(encryptionConfig.Data[keyKMSProviderConfig], vaultConfig); err != nil {
		return err
	}

	klog.Infof("kms is enabled: found config, now patching kube-apiserver pod")
	if err := addKMSPluginSidecarToPodSpec(podSpec, "kms-plugin", kmsPluginImage, vaultConfig, endpoint, keySecretID); err != nil {
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

type vaultConfiguration struct {
	RoleID    string
	Addr      string
	Namespace string
	KeyName   string
}

func addKMSPluginSidecarToPodSpec(podSpec *corev1.PodSpec, containerName string, image string, config *vaultConfiguration, endpoint, keySecretID string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            containerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/bin/sh", "-c"},
		Args: []string{fmt.Sprintf(`
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=transit \
	-transit-key=%s \
	-log-level=debug-extended \
	-approle-role-id=%s \
	-approle-secret-id-path=/etc/kubernetes/static-pod-resources/%s`,
			endpoint,
			config.Addr,
			config.Namespace,
			config.KeyName,
			config.RoleID,
			keySecretID),
		},
		// TODO: this volumeMount is used by kube-apiserver as well, so it's be present in the pod.Spec
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

	foundVolumeMount := false
	for _, volumeMount := range podSpec.Volumes {
		if volumeMount.Name == "kms-plugin-socket" {
			foundVolumeMount = true
		}
	}

	if !foundVolumeMount {
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
