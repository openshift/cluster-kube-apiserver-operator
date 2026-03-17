package targetconfigcontroller

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

// AddKMSPluginToPodSpec conditionally adds the KMS plugin volume mount to the specified container.
// It assumes the pod spec does not already contain the KMS volume or mount; no deduplication is performed.
// Deprecated: this is a temporary solution to get KMS TP v1 out. We should come up with a different approach afterwards.
func AddKMSPluginToPodSpec(podSpec *corev1.PodSpec, featureGateAccessor featuregates.FeatureGateAccess, secretLister corev1listers.SecretLister, targetNamespace string, kmsPluginImage string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	// if !featureGateAccessor.AreInitialFeatureGatesObserved() {
	// 	return nil
	// }
	//
	// featureGates, err := featureGateAccessor.CurrentFeatureGates()
	// if err != nil {
	// 	return fmt.Errorf("failed to get feature gates: %w", err)
	// }
	//
	// if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
	// 	klog.Infof("kms is disabled: feature gate %s is disabled", features.FeatureGateKMSEncryption)
	// 	return nil
	// }

	creds, err := secretLister.Secrets(targetNamespace).Get("vault-kms-credentials")
	if err != nil {
		klog.Infof("kms is disabled: could not get vault-kms-credentials secret: %v", err)
		return nil
	}

	// At this point we know we should deploy the KMS plugin
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

func AddKMSPluginToPodSpecFn(o *installerpod.InstallOptions) installerpod.PodMutationFunc {
	klog.Infof("fjb: in AddKMSPluginToPodSpecFn")
	kmsPluginImage := "quay.io/bertinatto/vault:v1"
	return func(pod *corev1.Pod) error {
		klog.Infof("fjb: running func in AddKMSPluginToPodSpecFn")
		creds, err := o.KubeClient.CoreV1().Secrets("openshift-kube-apiserver").Get(context.TODO(), "vault-kms-credentials", v1.GetOptions{})
		if err != nil {
			klog.Infof("kms is disabled: could not get vault-kms-credentials secret: %v", err)
			return nil
		}
		klog.Infof("kms is ENABLED")

		if err := addKMSPluginSidecarToPodSpec(&pod.Spec, "kms-plugin", kmsPluginImage, creds); err != nil {
			return err
		}

		if err := addKMSPluginVolumeAndMountToPodSpec(&pod.Spec, "kms-plugin"); err != nil {
			return err
		}

		if err := addKMSPluginVolumeAndMountToPodSpec(&pod.Spec, "kube-apiserver"); err != nil {
			return err
		}

		return nil
	}
}
