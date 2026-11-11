package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionLifecycleGroup [OCPFeatureGate:KMSEncryption][Serial][Timeout:180m][Suite:encryption-kms-3][Suite:encryption-kms-lifecycle]", func(ctx context.Context) {
		testKMSEncryptionLifecycleGroup(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionLifecycleGroup runs negative preflights, enables valid KMS,
// in-place updates, and remote-key rotation as one ordered composite, leaving
// KMS enabled on success.
func testKMSEncryptionLifecycleGroup(ctx context.Context, t testing.TB) {
	lifecycle, rotation := librarykms.LifecycleGroupScenarios(ctx, t)
	library.TestKMSLifecycleGroup(ctx, t, lifecycle, rotation)
}
