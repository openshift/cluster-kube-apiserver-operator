package targetconfigcontroller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

func addKMSPluginSidecar(podSpec *corev1.PodSpec, kmsPluginImage string, secretLister corev1listers.SecretLister, targetNamespace string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	for _, container := range podSpec.Containers {
		if container.Name == "kms-plugin" {
			return nil
		}
	}

	creds, err := secretLister.Secrets(targetNamespace).Get("vault-kms-credentials")
	if err != nil {
		klog.Warningf("kms is disabled: could not find vault-kms-credentials secret: %v", err)
		return nil
	}

	argsFmt := `
echo "%s" > /tmp/secret-id
exec /vault-kube-kms \
-listen-address=unix:///var/run/kmsplugin/kms.sock \
-vault-address=%s \
-vault-namespace=%s \
-transit-mount=transit \
-transit-key=%s \
-log-level=debug-extended \
-approle-role-id=%s \
-approle-secret-id-path=/tmp/secret-id`

	args := fmt.Sprintf(argsFmt,
		string(creds.Data["VAULT_SECRET_ID"]),
		string(creds.Data["VAULT_ADDR"]),
		string(creds.Data["VAULT_NAMESPACE"]),
		string(creds.Data["VAULT_KEY_NAME"]),
		string(creds.Data["VAULT_ROLE_ID"]))

	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            "kms-plugin",
		Image:           kmsPluginImage,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/bin/sh", "-c"},
		Args:            []string{args},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "kms-plugin-socket",
				MountPath: "/var/run/kmsplugin",
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

func addKMSPluginVolumeAndMountToPodSpec(podSpec *corev1.PodSpec, containerName string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

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
