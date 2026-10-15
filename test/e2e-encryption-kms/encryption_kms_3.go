package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSPreflightLifecycle [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-3]", func(ctx context.Context) {
		testKMSPreflightLifecycle(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionRotation [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-3]", func(ctx context.Context) {
		testKMSEncryptionRotation(ctx, g.GinkgoTB())
	})
})

// testKMSPreflightLifecycle tests the full KMS preflight lifecycle across
// kube-apiserver, oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Applies invalid KMS configs and asserts preflight failures
// 2. Recovers directly from the last invalid config to a valid KMS provider
// 3. Verifies encryption is established with the new key
// 4. Performs an in-place update of the KMS plugin image
// 5. Switches to identity (off) to verify resources are re-written unencrypted
func testKMSPreflightLifecycle(ctx context.Context, t testing.TB) {
	library.TestKMSPreflightLifecycle(ctx, t, librarykms.KMSPreflightLifecycleScenarios(ctx, t)...)
}

// testKMSEncryptionRotation encrypts with Vault KMS, rotates the Vault transit
// remote key, waits for each operator's remigration, verifies ciphertext changed,
// and reverts to identity.
func testKMSEncryptionRotation(ctx context.Context, t testing.TB) {
	library.TestEncryptionRotation(ctx, t, librarykms.EncryptionRotationScenarios(ctx, t)...)
}
