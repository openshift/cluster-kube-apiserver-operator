package plugins

import (
	"fmt"
	"path/filepath"
	"regexp"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const credentialsDir = "/etc/kubernetes/static-pod-resources/secrets/encryption-config"

var kmsEndpointRegexp = regexp.MustCompile(`^unix:///var/run/kmsplugin/kms-(\d+)\.sock$`)

type VaultSidecarProvider struct {
	Config *configv1.VaultKMSConfig
	RoleID string
}

func (v *VaultSidecarProvider) BuildSidecarContainer(name string, kmsConfig *apiserverv1.KMSConfiguration) (corev1.Container, error) {
	if v.Config == nil {
		return corev1.Container{}, fmt.Errorf("vault config cannot be nil")
	}
	if v.RoleID == "" {
		return corev1.Container{}, fmt.Errorf("vault role ID cannot be empty")
	}

	keyID, err := parseKeyID(kmsConfig.Endpoint)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to parse key ID from endpoint: %w", err)
	}

	credentialsFile := filepath.Join(credentialsDir, fmt.Sprintf("kms-secret-data-%s", keyID))

	args := fmt.Sprintf(`
	CREDS=$(cat %s)
	SECRET_ID=${CREDS#*\"VAULT_SECRET_ID\":\"}
	SECRET_ID=${SECRET_ID%%%%\"*}
	printf '%%s' "$SECRET_ID" > /tmp/secret-id
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=%s \
	-transit-key=%s \
	-approle-role-id=%s \
	-approle-secret-id-path=/tmp/secret-id`,
		credentialsFile,
		kmsConfig.Endpoint,
		v.Config.VaultAddress,
		v.Config.VaultNamespace,
		v.Config.TransitMount,
		v.Config.TransitKey,
		v.RoleID,
	)

	return corev1.Container{
		Name:    name,
		Image:   v.Config.KMSPluginImage,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{args},
	}, nil
}

func parseKeyID(endpoint string) (string, error) {
	matches := kmsEndpointRegexp.FindStringSubmatch(endpoint)
	if matches == nil {
		return "", fmt.Errorf("unexpected KMS endpoint format: %s", endpoint)
	}
	return matches[1], nil
}
