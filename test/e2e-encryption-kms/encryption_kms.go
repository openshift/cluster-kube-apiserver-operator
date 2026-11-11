package e2e_encryption_kms

import (
	"context"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"

	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:150m]", func(ctx context.Context) {
		testKMSEncryptionOnOff(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:150m]", func(ctx context.Context) {
		testKMSEncryptionProvidersMigration(ctx, g.GinkgoTB())
	})

})

// testKMSEncryptionOnOff tests KMS encryption on/off cycle across kube-apiserver,
// oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 2. Enables KMS encryption and verifies all resources are encrypted
// 3. Disables encryption (Identity) and verifies all resources are NOT encrypted
// 4. Re-enables KMS encryption and verifies all resources are encrypted again
// 5. Disables encryption (Identity) again and verifies all resources are NOT encrypted again
func testKMSEncryptionOnOff(ctx context.Context, t testing.TB) {
	library.TestEncryptionTurnOnAndOff(ctx, t, librarykms.EncryptionTurnOnAndOffScenarios(ctx, t)...)
	waitForEncryptionOperatorsStable(ctx, t)
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES encryption providers
// across kube-apiserver, oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 2. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 3. Shuffles the selected AES provider with KMS to create a randomized migration order
// 4. Applies one cluster-wide APIServer config update per step and waits per operator in parallel
// 5. Verifies each resource is correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	library.TestEncryptionProvidersMigration(ctx, t, librarykms.EncryptionProvidersMigrationScenarios(ctx, t)...)
	waitForEncryptionOperatorsStable(ctx, t)
}

// waitForEncryptionOperatorsStable blocks until kube-apiserver and the
// operators mirroring the cluster-wide encryption config finish their
// rollouts. The waits inside the library scenarios only cover the key
// migration: right after the final identity key is reported migrated, the
// operator's encryption controllers drop the now-unneeded read key, which
// rewrites the encryption-config secret and forces one more kube-apiserver
// static pod revision and a full master-by-master NodeInstaller roll. Without
// waiting here the suite exits mid-roll and the post-suite e2e-analysis
// check (verify operator conditions kube-apiserver) fails on the transient
// Progressing=True condition, see OCPBUGS-139148. The extra wait is mostly
// absorbed by the next serial test's own key-migration waits; only the last
// test of a suite pays for the full rollout.
func waitForEncryptionOperatorsStable(ctx context.Context, t testing.TB) {
	operatorencryption.WaitForEncryptionOperatorsStable(ctx, t, 30*time.Minute)
}
