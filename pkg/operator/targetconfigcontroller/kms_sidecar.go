package targetconfigcontroller

import (
	"fmt"
	"slices"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/apis/apiserver"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

var configScheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(configScheme, serializer.EnableStrict)

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
		klog.Infof("kms is disabled: secret openshift-config/encryption-config not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("kms is disabled: failed to get encryption-config-openshift-kube-apiserver secret: %v", err)
		return nil
	}

	encryptionConfigBytes := encryptionConfig.Data["encryption-config"]
	obj, _, err := codecs.UniversalDeserializer().Decode(encryptionConfigBytes, nil, nil)
	if err != nil {
		return err
	}

	// FIXME: only Vault KMS plugin is supported for now, so any KMS configuration implies Vault
	config := obj.(*apiserver.EncryptionConfiguration)
	shouldSetupVault := slices.ContainsFunc(config.Resources, func(resource apiserver.ResourceConfiguration) bool {
		return slices.ContainsFunc(resource.Providers, func(provider apiserver.ProviderConfiguration) bool {
			if provider.KMS != nil {
				return true
			}
			return false
		})
	})

	if !shouldSetupVault {
		klog.Infof("kms is disabled: vault should not be set up")
		return nil
	}

	// is kms specified?
	// return if not
	// get passwrod: vault-kms-credentials
	klog.Infof("kms is enabled: trying to find credentials")

	// FIXME: credentials will be available in the encryptionConfiguration, but they are not there yet.
	// Get them from a custom secret for now
	creds, err := secretLister.Secrets("openshift-config").Get("vault-kms-credentials")
	if apierrors.IsNotFound(err) {
		klog.Infof("kms is disabled: secret openshift-config/vault-kms-credentials not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("kms is disabled: failed to get vault-kms-credentials secret: %v", err)
		return nil
	}

	klog.Infof("kms is enabled: found redentials, now patchin kube-apiserver pod")
	if err := addKMSPluginSidecarToPodSpec(podSpec, "kms-plugin", kmsPluginImage, creds); err != nil {
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

func addKMSPluginSidecarToPodSpec(podSpec *corev1.PodSpec, containerName string, image string, creds *corev1.Secret) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            containerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/bin/sh", "-c"},
		Args: []string{fmt.Sprintf(`
	echo "%s" > /tmp/secret-id
	exec /vault-kube-kms \
	-listen-address=unix:///var/run/kmsplugin/kms.sock \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=transit \
	-transit-key=%s \
	-log-level=debug-extended \
	-approle-role-id=%s \
	-approle-secret-id-path=/tmp/secret-id`,
			string(creds.Data["VAULT_SECRET_ID"]),
			string(creds.Data["VAULT_ADDR"]),
			string(creds.Data["VAULT_NAMESPACE"]),
			string(creds.Data["VAULT_KEY_NAME"]),
			string(creds.Data["VAULT_ROLE_ID"])),
		},
		// TODO: this volumeMount is used by kube-apiserver as well, so it's be present in the pod.Spec
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "resource-dir",
				MountPath: "/etc/kubernetes/static-pod-resources",
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptrBool(true),
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

func ptrBool(b bool) *bool {
	return &b
}

// func AddKMSPluginToPodSpecFn(o *installerpod.InstallOptions) installerpod.PodMutationFunc {
// 	klog.Infof("fjb: in AddKMSPluginToPodSpecFn")
// 	kmsPluginImage := "quay.io/bertinatto/vault:v1"
// 	return func(pod *corev1.Pod) error {
// 		klog.Infof("fjb: running func in AddKMSPluginToPodSpecFn")
// 		creds, err := o.KubeClient.CoreV1().Secrets("openshift-kube-apiserver").Get(context.TODO(), "vault-kms-credentials", v1.GetOptions{})
// 		if err != nil {
// 			klog.Infof("kms is disabled: could not get vault-kms-credentials secret: %v", err)
// 			return nil
// 		}
// 		klog.Infof("kms is ENABLED")
//
// 		if err := addKMSPluginSidecarToPodSpec(&pod.Spec, "kms-plugin", kmsPluginImage, creds, 0 /* FIXME: dummy condition*/); err != nil {
// 			return err
// 		}
//
// 		if err := addKMSPluginVolumeAndMountToPodSpec(&pod.Spec, "kms-plugin"); err != nil {
// 			return err
// 		}
//
// 		if err := addKMSPluginVolumeAndMountToPodSpec(&pod.Spec, "kube-apiserver"); err != nil {
// 			return err
// 		}
//
// 		return nil
// 	}
// }
