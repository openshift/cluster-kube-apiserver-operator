package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionMigrationGroup [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2][Suite:encryption-kms-migration]", func(ctx context.Context) {
		testKMSEncryptionMigrationGroup(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionMigrationGroup runs manual preflight deploy then KMS↔KMS
// migration as one ordered composite, leaving the final KMS provider enabled.
// In-place update coverage lives in the lifecycle group.
func testKMSEncryptionMigrationGroup(ctx context.Context, t testing.TB) {
	deploy, migration := librarykms.MigrationGroupScenarios(ctx, t)
	library.TestKMSMigrationGroup(ctx, t, deploy, migration...)
}
