package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	g "github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	vaultNamespace         = "vault-kms"
	vaultCredentialsSecret = "vault-credentials"
	vaultAppRoleSecretName = "vault-approle-secret"
	vaultKMSPluginImage    = "registry.ci.openshift.org/control-plane-custom-builds/vault-kube-kms@sha256:33599dd6eee61dcf9a60138759fafda3d88593a3c2072585156882c6b5bd3fa5"
	vaultAddress           = "https://vault.vault-kms.svc:8200"
	vaultTransitKey        = "kms-key"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func() {
		testKMSEncryptionOnOff(g.GinkgoTB())
	})

	g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func() {
		testKMSEncryptionProvidersMigration(g.GinkgoTB())
	})
})

// testKMSEncryptionOnOff tests KMS encryption on/off cycle with the Vault provider.
// Vault is deployed by the CI step (etcd-encryption-vault-install) before this
// test runs. The operator injects the KMS plugin sidecar based on the APIServer CR.
func testKMSEncryptionOnOff(t testing.TB) {
	library.TestEncryptionTurnOnAndOff(t, library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider: library.EncryptionProviderConfig{
			Type:        configv1.EncryptionTypeKMS,
			ConfigureFn: configureVaultKMS,
		},
	})
}

// testKMSEncryptionProvidersMigration tests migration between KMS (Vault) and
// AES encryption providers. Vault is deployed by the CI step.
func testKMSEncryptionProvidersMigration(t testing.TB) {
	library.TestEncryptionProvidersMigration(t, library.ProvidersMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProviders: library.ShuffleEncryptionProviders([]library.EncryptionProviderConfig{
			{Type: configv1.EncryptionTypeKMS, ConfigureFn: configureVaultKMS},
			library.NewStaticEncryptionProvider(library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))]),
		}),
	})
}

// configureVaultKMS reads credentials from the vault-credentials secret
// (created by the CI step), creates the AppRole secret in openshift-config,
// and patches the APIServer CR with the Vault KMS configuration.
func configureVaultKMS(t testing.TB, cs library.ClientSet) {
	t.Helper()
	ctx := context.Background()

	creds, err := cs.Kube.CoreV1().Secrets(vaultNamespace).Get(ctx, vaultCredentialsSecret, metav1.GetOptions{})
	require.NoError(t, err, "failed to read %s/%s secret (was the vault-install CI step run?)", vaultNamespace, vaultCredentialsSecret)

	appRoleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vaultAppRoleSecretName,
			Namespace: "openshift-config",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"roleID":   creds.Data["role-id"],
			"secretID": creds.Data["secret-id"],
		},
	}
	_, err = cs.Kube.CoreV1().Secrets("openshift-config").Create(ctx, appRoleSecret, metav1.CreateOptions{})
	if err != nil {
		_, err = cs.Kube.CoreV1().Secrets("openshift-config").Update(ctx, appRoleSecret, metav1.UpdateOptions{})
		require.NoError(t, err)
	}
	t.Logf("Created/updated AppRole secret %s in openshift-config", vaultAppRoleSecretName)

	library.UpdateKMSConfig(t, configv1.KMSPluginConfig{
		Type: configv1.VaultKMSProvider,
		Vault: configv1.VaultKMSPluginConfig{
			KMSPluginImage: vaultKMSPluginImage,
			VaultAddress:   vaultAddress,
			TransitMount:   "transit",
			TransitKey:     vaultTransitKey,
			Authentication: configv1.VaultAuthentication{
				Type: configv1.VaultAuthenticationTypeAppRole,
				AppRole: configv1.VaultAppRoleAuthentication{
					Secret: configv1.VaultSecretReference{Name: vaultAppRoleSecretName},
				},
			},
		},
	})
}
