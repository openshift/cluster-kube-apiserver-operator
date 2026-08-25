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

	g.It("TestKMSPreflightDeploy [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSPreflightDeploy(ctx, g.GinkgoTB())
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
// 5. Switches to dentity (off) to verify the resources are re-written unencrypted
func testKMSEncryptionKMSToKMSMigration(ctx context.Context, t testing.TB) {
	library.TestEncryptionProvidersMigration(ctx, t, librarykms.EncryptionKMSToKMSMigrationScenarios(ctx, t)...)
}

func testKMSPreflightDeploy(ctx context.Context, t testing.TB) {
	library.TestPreflightDeployAndPodMatchesOperand(ctx, t, librarykms.PreflightDeployScenario(ctx, t))
}
