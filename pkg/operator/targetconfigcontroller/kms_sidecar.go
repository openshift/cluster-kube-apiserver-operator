package targetconfigcontroller

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

const (
	kmsPluginContainerName  = "kms-plugin"
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

	_, err = secretLister.Secrets(targetNamespace).Get(vaultKMSCredentialsName)
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

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            kmsPluginContainerName,
		Image:           kmsPluginImage,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/bin/sh", "-c"},
		Args: []string{
			`echo "$VAULT_SECRET_ID" > /tmp/secret-id
exec /vault-kube-kms \
  -listen-address=unix://` + kmsPluginSocketPath + `/kms.sock \
  -vault-address=$VAULT_ADDR \
  -vault-namespace=$VAULT_NAMESPACE \
  -transit-mount=transit \
  -transit-key=$VAULT_KEY_NAME \
  -log-level=debug-extended \
  -approle-role-id=$VAULT_ROLE_ID \
  -approle-secret-id-path=/tmp/secret-id`,
		},
		Env: []corev1.EnvVar{
			{
				Name: "VAULT_ROLE_ID",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: vaultKMSCredentialsName},
						Key:                  "VAULT_ROLE_ID",
					},
				},
			},
			{
				Name: "VAULT_SECRET_ID",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: vaultKMSCredentialsName},
						Key:                  "VAULT_SECRET_ID",
					},
				},
			},
			{
				Name: "VAULT_ADDR",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: vaultKMSCredentialsName},
						Key:                  "VAULT_ADDR",
					},
				},
			},
			{
				Name: "VAULT_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: vaultKMSCredentialsName},
						Key:                  "VAULT_NAMESPACE",
					},
				},
			},
			{
				Name: "VAULT_KEY_NAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: vaultKMSCredentialsName},
						Key:                  "VAULT_KEY_NAME",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "kms-plugin-socket",
				MountPath: kmsPluginSocketPath,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptrBool(true),
		},
	})

	return nil
}

func ptrBool(b bool) *bool {
	return &b
}
