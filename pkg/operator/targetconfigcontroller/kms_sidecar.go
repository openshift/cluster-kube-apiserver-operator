package targetconfigcontroller

import (
	"fmt"

	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

const (
	kmsPluginContainerName  = "kms-plugin"
	kmsPluginSocketVolume   = "kms-plugin-socket"
	kmsPluginSocketPath     = "/var/run/kmsplugin"
	vaultKMSCredentialsName = "vault-kms-credentials"
	quayPullSecretName      = "quay-pull-secret"
)

func addKMSPluginSidecar(podSpec *corev1.PodSpec, kmsPluginImage string, featureGateAccessor featuregates.FeatureGateAccess, secretLister corev1listers.SecretLister, targetNamespace string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil
	}

	// featureGates, err := featureGateAccessor.CurrentFeatureGates()
	// if err != nil {
	// 	return fmt.Errorf("failed to get feature gates: %w", err)
	// }

	// if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
	// 	return nil
	// }

	for _, container := range podSpec.Containers {
		if container.Name == kmsPluginContainerName {
			return nil
		}
	}

	creds, err := secretLister.Secrets(targetNamespace).Get(vaultKMSCredentialsName)
	if err != nil {
		klog.Warningf("kms is disabled: could not find vault-kms-credentials secret: %v", err)
		return nil
	}

	_, err = secretLister.Secrets(targetNamespace).Get(quayPullSecretName)
	if err != nil {
		klog.Warningf("kms is disabled: could not find quay-pull-secret secret: %v", err)
		return nil
	}

	// For Vault KMS plugin image pull authentication
	podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: quayPullSecretName})

	argsFmt := `
echo "%s" > /tmp/secret-id
exec /vault-kube-kms \
-listen-address=unix://%s/kms.sock \
-vault-address=%s \
-vault-namespace=%s \
-transit-mount=transit \
-transit-key=%s \
-log-level=debug-extended \
-approle-role-id=%s \
-approle-secret-id-path=/tmp/secret-id`

	args := fmt.Sprintf(argsFmt,
		string(creds.Data["VAULT_SECRET_ID"]),
		kmsPluginSocketPath,
		string(creds.Data["VAULT_ADDR"]),
		string(creds.Data["VAULT_NAMESPACE"]),
		string(creds.Data["VAULT_KEY_NAME"]),
		string(creds.Data["VAULT_ROLE_ID"]))

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            kmsPluginContainerName,
		Image:           kmsPluginImage,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/bin/sh", "-c"},
		Args:            []string{args},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "kms-plugin-socket",
				MountPath: kmsPluginSocketPath,
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptrBool(true),
		},
	})

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

	return nil
}

func ptrBool(b bool) *bool {
	return &b
}
