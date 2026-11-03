package e2e_encryption_kms

import (
	"context"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSPerfEncryption [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-perf]", func(ctx context.Context) {
		testKMSPerfEncryption(ctx, g.GinkgoTB())
	})
})

// testKMSPerfEncryption measures Vault KMS encryption migration time across
// kube-apiserver, oauth-apiserver, and openshift-apiserver.
func testKMSPerfEncryption(ctx context.Context, tt testing.TB) {
	library.TestKMSPerfEncryption(ctx, tt, librarykms.EncryptionPerfScenarios(ctx, tt, librarykms.EncryptionPerfConfig{
		DBLoaderWorkers:       operatorencryption.DefaultDBLoaderWorkers,
		DBLoaderFunc:          operatorencryption.DefaultDBLoader(),
		AssertDBPopulatedFunc: operatorencryption.AssertDBPopulated,
		AssertMigrationTime:   operatorencryption.AssertMigrationTime,
	})...)
}
