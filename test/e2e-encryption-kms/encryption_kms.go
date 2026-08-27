package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionOnOff(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionProvidersMigration(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionOnOff tests KMS encryption on/off cycle across kube-apiserver,
// oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Runs preflight with the default Vault provider to verify the KMS plugin is reachable
// 2. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 3. Enables KMS encryption and verifies all resources are encrypted
// 4. Disables encryption (Identity) and verifies all resources are NOT encrypted
// 5. Re-enables KMS encryption and verifies all resources are encrypted again
// 6. Disables encryption (Identity) again and verifies all resources are NOT encrypted again
func testKMSEncryptionOnOff(ctx context.Context, t testing.TB) {
	testKMSPreflightDeploy(ctx, t, librarykms.DefaultVaultEncryptionProvider(ctx, t))
	library.TestEncryptionTurnOnAndOff(ctx, t, librarykms.EncryptionTurnOnAndOffScenarios(ctx, t)...)
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES encryption providers
// across kube-apiserver, oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Runs preflight with the default Vault provider to verify the KMS plugin is reachable
// 2. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 3. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 4. Shuffles the selected AES provider with KMS to create a randomized migration order
// 5. Applies one cluster-wide APIServer config update per step and waits per operator in parallel
// 6. Verifies each resource is correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	testKMSPreflightDeploy(ctx, t, librarykms.DefaultVaultEncryptionProvider(ctx, t))
	library.TestEncryptionProvidersMigration(ctx, t, librarykms.EncryptionProvidersMigrationScenarios(ctx, t)...)
}
