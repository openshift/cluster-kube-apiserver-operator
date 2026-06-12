package kms

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	defaultVaultNamespace          = "vault-kms"
	defaultVaultPodName            = "vault-0"
	defaultVaultCredentialsSecret  = "vault-credentials"
	defaultVaultAppRoleSecretName  = "vault-approle-secret"
	defaultVaultConfigMapName      = "vault-ca-bundle"
	defaultFAKEVaultKMSPluginImage = "quay.io/openshifttest/mock-kms-plugin@sha256:958a2f8276037468aa47dc2137d3c30dfcd96489455eddb2fe655f8168a57622"
	defaultVaultKMSPluginImage     = "registry.ci.openshift.org/control-plane-custom-builds/vault-kube-kms@sha256:33599dd6eee61dcf9a60138759fafda3d88593a3c2072585156882c6b5bd3fa5"
	defaultVaultAddress            = "https://vault.vault-kms.svc:8200"
	defaultVaultEnterpriseNS       = "admin"
	defaultVaultTransitMount       = "transit"
	defaultVaultTransitKey         = "kms-key"
	defaultAppRoleTargetNamespace  = "openshift-config"
	vaultCommandTimeout            = 30 * time.Second
)

// DefaultVaultEncryptionProvider returns a ready-to-use Vault KMS EncryptionProvider for e2e tests.
func DefaultVaultEncryptionProvider(ctx context.Context, t testing.TB) library.EncryptionProvider {
	return library.EncryptionProvider{
		APIServerEncryption: DefaultVaultKMSPluginConfig,
		Setup:               ensureVaultAppRoleSecret(defaultVaultNamespace, defaultVaultAppRoleSecretName),
	}
}

var DefaultFakeVaultEncryptionProvider = library.EncryptionProvider{
	APIServerEncryption: DefaultFakeKMSPluginConfig,
	Setup:               ensureVaultAppRoleSecret(defaultVaultNamespace, defaultVaultAppRoleSecretName),
}

// DefaultVaultKMSPluginConfig is the standard Vault KMS encryption config
// used by CI e2e tests.
var DefaultVaultKMSPluginConfig = configv1.APIServerEncryption{
	Type: configv1.EncryptionTypeKMS,
	KMS: configv1.KMSPluginConfig{
		Type: configv1.VaultKMSProvider,
		Vault: configv1.VaultKMSPluginConfig{
			KMSPluginImage: defaultVaultKMSPluginImage,
			VaultAddress:   defaultVaultAddress,
			VaultNamespace: defaultVaultEnterpriseNS,
			TransitMount:   defaultVaultTransitMount,
			TransitKey:     defaultVaultTransitKey,
			Authentication: configv1.VaultAuthentication{
				Type: configv1.VaultAuthenticationTypeAppRole,
				AppRole: configv1.VaultAppRoleAuthentication{
					Secret: configv1.VaultSecretReference{Name: defaultVaultAppRoleSecretName},
				},
			},
			TLS: configv1.VaultTLSConfig{
				CABundle: configv1.VaultConfigMapReference{
					Name: defaultVaultConfigMapName,
				},
				ServerName: fmt.Sprintf("vault.%s.svc", defaultVaultNamespace),
			},
		},
	},
}

// DefaultFakeKMSPluginConfig is a fake Vault KMS configuration used by unit tests.
var DefaultFakeKMSPluginConfig = configv1.APIServerEncryption{
	Type: configv1.EncryptionTypeKMS,
	KMS: configv1.KMSPluginConfig{
		Type: configv1.VaultKMSProvider,
		Vault: configv1.VaultKMSPluginConfig{
			KMSPluginImage: defaultFAKEVaultKMSPluginImage,
			VaultAddress:   "https://vault.example.com",
			Authentication: configv1.VaultAuthentication{
				Type: configv1.VaultAuthenticationTypeAppRole,
				AppRole: configv1.VaultAppRoleAuthentication{
					Secret: configv1.VaultSecretReference{Name: defaultVaultAppRoleSecretName},
				},
			},
			TransitKey:   "test-transit-key",
			TransitMount: defaultVaultTransitMount,
		},
	},
}

func ensureVaultAppRoleSecret(vaultNamespace, appRoleSecretName string) func(ctx context.Context, t testing.TB) {
	return func(ctx context.Context, t testing.TB) {
		t.Helper()
		cs := library.GetClients(t)

		creds, err := cs.Kube.CoreV1().Secrets(vaultNamespace).Get(ctx, defaultVaultCredentialsSecret, metav1.GetOptions{})
		require.NoError(t, err, "failed to read %s/%s secret (was the vault-install CI step run?)", vaultNamespace, defaultVaultCredentialsSecret)

		required := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appRoleSecretName,
				Namespace: defaultAppRoleTargetNamespace,
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"role-id":   creds.Data["role-id"],
				"secret-id": creds.Data["secret-id"],
			},
		}
		recorder := events.NewInMemoryRecorder("vault-approle-secret-setup", clock.RealClock{})
		_, changed, err := resourceapply.ApplySecret(ctx, cs.Kube.CoreV1(), recorder, required)
		require.NoError(t, err, "failed to apply AppRole secret")
		t.Logf("Applied AppRole secret %s in %s (changed=%v)", appRoleSecretName, defaultAppRoleTargetNamespace, changed)
	}
}

func ForceVaultKeyRotation() library.ForceRotationFunc {
	return RotateVaultTransitKey
}

// RotateVaultTransitKey rotates the Vault transit encryption key. All old key versions are retained.
// Reference: https://developer.hashicorp.com/vault/api-docs/secret/transit#rotate-key
// Steps:
// 1. Get initial key version
// 2. Execute 'vault write -f transit/keys/<key-name>/rotate' via oc exec
// 3. Get new key version and validate it increased
func RotateVaultTransitKey(t testing.TB, ctx context.Context) {
	t.Helper()

	initialVersion := getCurrentKeyVersion(ctx, t)
	rotateKey(ctx, t)
	newVersion := getCurrentKeyVersion(ctx, t)

	require.Greater(t, newVersion, initialVersion, "rotation failed: version did not increase (before=%d, after=%d)", initialVersion, newVersion)
}

// rotateKey executes the vault key rotation command
func rotateKey(ctx context.Context, t testing.TB) {
	t.Helper()
	commandCtx, cancel := context.WithTimeout(ctx, vaultCommandTimeout)
	defer cancel()

	// Command: vault write -f transit/keys/<key-name>/rotate
	// Reference: https://developer.hashicorp.com/vault/api-docs/secret/transit#rotate-key
	cmd := exec.CommandContext(commandCtx, "oc", "exec", defaultVaultPodName, "-n", defaultVaultNamespace, "--",
		"vault", "write", "-f", fmt.Sprintf("transit/keys/%s/rotate", defaultVaultTransitKey))

	t.Logf("Executing: %s", cmd.String())
	output, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		require.NoError(t, err, "vault key rotation failed, stderr: %s", string(ee.Stderr))
	}
	require.NoError(t, err, "vault key rotation failed")
	t.Logf("Command output: %s", string(output))
}

// getCurrentKeyVersion retrieves the current (latest) key version
func getCurrentKeyVersion(ctx context.Context, t testing.TB) int {
	t.Helper()
	commandCtx, cancel := context.WithTimeout(ctx, vaultCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "oc", "exec", defaultVaultPodName, "-n", defaultVaultNamespace, "--",
		"vault", "read", "-field=latest_version", fmt.Sprintf("transit/keys/%s", defaultVaultTransitKey))

	t.Logf("Executing: %s", cmd.String())
	output, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		require.NoError(t, err, "failed to read key version, stderr: %s", string(ee.Stderr))
	}
	require.NoError(t, err, "failed to read key version")
	t.Logf("Command output: %s", string(output))

	version, err := strconv.Atoi(strings.TrimSpace(string(output)))
	require.NoError(t, err, "failed to parse key version from output: %q", string(output))

	return version
}


