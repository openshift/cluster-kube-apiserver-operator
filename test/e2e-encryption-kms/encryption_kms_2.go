package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})

	g.It("TestKMSPreflightDeploy [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSPreflightDeploy(ctx, g.GinkgoTB())
	})

	g.It("TestKMSPreflightNegative [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSPreflightNegative(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionKMSToKMSMigration tests migration between two distinct KMS providers
// (default Vault instance and secondary Vault instance) across kube-apiserver,
// oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 2. Shuffles the two KMS providers to create a randomized migration order
// 3. Migrates between the two KMS providers (KMS-to-KMS) in the shuffled order
// 4. Verifies each resource is correctly encrypted with the active KMS provider after each migration
// 5. Switches to identity (off) to verify the resources are re-written unencrypted
func testKMSEncryptionKMSToKMSMigration(ctx context.Context, t testing.TB) {
	library.TestEncryptionProvidersMigration(ctx, t, librarykms.EncryptionKMSToKMSMigrationScenarios(ctx, t)...)
}

func testKMSPreflightDeploy(ctx context.Context, t testing.TB) {
	library.TestPreflightDeployAndPodMatchesOperand(ctx, t, librarykms.PreflightDeployScenario(ctx, t))
}

// testKMSPreflightNegative applies invalid Vault KMS configs and asserts preflight
// failure (Degraded/Failed) without creating a new encryption key.
func testKMSPreflightNegative(ctx context.Context, t testing.TB) {
	const invalidAppRoleSecret = "vault-approle-secret-invalid-preflight"
	librarykms.TestKMSPreflightNegative(ctx, t, librarykms.KMSPreflightNegativeScenarios(ctx, t,
		librarykms.VaultNegativeCase{Name: "invalid-address", Mutate: func(vault *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(vault.Object, "https://192.0.2.1:8200", "spec", "vaultAddress")
		}},
		librarykms.VaultNegativeCase{Name: "invalid-address-2", Mutate: func(vault *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(vault.Object, "https://192.0.2.2:8200", "spec", "vaultAddress")
		}},
		librarykms.VaultNegativeCase{Name: "invalid-keypath", Mutate: func(vault *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(vault.Object, "transit/keys/does-not-exist", "spec", "vaultKeyPath")
		}},
		librarykms.VaultNegativeCase{
			Name: "invalid-approle",
			Mutate: func(vault *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(vault.Object, invalidAppRoleSecret, "spec", "authentication", "appRole", "secret", "name")
			},
			Setup: func(ctx context.Context, t testing.TB, clients library.ClientSet) {
				librarykms.EnsureInvalidVaultAppRoleSecret(ctx, t, clients, invalidAppRoleSecret, "invalid-secret-id-for-preflight-test")
			},
		},
		librarykms.VaultNegativeCase{Name: "invalid-tls", Mutate: func(vault *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(vault.Object, "vault.invalid.example", "spec", "tls", "serverName")
		}},
		librarykms.VaultNegativeCase{Name: "invalid-image", Mutate: func(vault *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(vault.Object, "quay.io/openshifttest/vault-kube-kms@sha256:0000000000000000000000000000000000000000000000000000000000000000", "status", "kmsPluginImage")
		}},
	)...)
}
