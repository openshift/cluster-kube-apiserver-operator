package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionKMSToKMSMigration tests migration between two distinct KMS providers
// (default Vault instance and secondary Vault instance) across kube-apiserver,
// oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Runs preflight for both Vault providers to verify each KMS plugin is reachable
// 2. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 3. Shuffles the two KMS providers to create a randomized migration order
// 4. Migrates between the two KMS providers (KMS-to-KMS) in the shuffled order
// 5. Verifies each resource is correctly encrypted with the active KMS provider after each migration
// 6. Switches to identity (off) to verify the resources are re-written unencrypted
func testKMSEncryptionKMSToKMSMigration(ctx context.Context, t testing.TB) {
	testKMSPreflightDeploy(ctx, t, librarykms.DefaultVaultEncryptionProvider(ctx, t))
	testKMSPreflightDeploy(ctx, t, librarykms.SecondaryVaultEncryptionProvider(ctx, t))
	library.TestEncryptionProvidersMigration(ctx, t, librarykms.EncryptionKMSToKMSMigrationScenarios(ctx, t)...)
}

// testKMSPreflightDeploy deploys the kms-preflight pod wired with the given provider,
// validates the pod succeeds (config hash, result, remote key ID conditions), and
// asserts no pod spec drift vs the live kube-apiserver operand pods.
func testKMSPreflightDeploy(ctx context.Context, t testing.TB, provider library.EncryptionProvider) {
	t.Helper()
	scenario := librarykms.PreflightDeployScenario(ctx, t)
	scenario.EncryptionProvider = provider
	library.TestPreflightDeployAndPodMatchesOperand(ctx, t, scenario)
}
