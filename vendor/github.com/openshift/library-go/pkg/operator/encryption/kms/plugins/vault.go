package plugins

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

type VaultSidecarProvider struct {
	Config *configv1.VaultKMSConfig
	// TODO: this is temporary. The credentials will be in a key in the encryption-configuration secret
	Credentials *corev1.Secret
}

func (v *VaultSidecarProvider) BuildSidecarContainer(name string, kmsConfig *apiserverv1.KMSConfiguration) (corev1.Container, error) {
	if v.Config == nil {
		return corev1.Container{}, fmt.Errorf("vault config cannot be nil")
	}
	if v.Credentials == nil {
		return corev1.Container{}, fmt.Errorf("vault credentials cannot be nil")
	}

	// TODO: figure out how to enable debug mode and add: -log-level=debug-extended

	args := fmt.Sprintf(`
	echo "%s" > /tmp/secret-id
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=%s \
	-transit-key=%s \
	-approle-role-id=%s \
	-approle-secret-id-path=/tmp/secret-id`,
		v.Credentials.Data["VAULT_SECRET_ID"], // FIXME: this is temporary until the credentials are store in the encryption-configuration secret. This leaks the secret-id in the pod manifest
		kmsConfig.Endpoint,
		v.Config.VaultAddress,
		v.Config.VaultNamespace,
		v.Config.TransitMount,
		v.Config.TransitKey,
		v.Credentials.Data["VAULT_ROLE_ID"], // FIXME: this is temporary until the credentials are store in the encryption-configuration secret. This leaks the app-role in the pod manifest
	)

	return corev1.Container{
		Name:    name,
		Image:   v.Config.KMSPluginImage,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{args},
	}, nil
}
