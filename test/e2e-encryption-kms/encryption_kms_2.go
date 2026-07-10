package e2e_encryption_kms

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionKMSToKMSMigration tests migration between two distinct KMS providers
// (default Vault instance and secondary Vault instance).
// This test:
// 1. Shuffles the two KMS providers to create a randomized migration order
// 2. Migrates between the providers in the shuffled order
// 3. Verifies secret is correctly encrypted after each migration
// 4. Switches to identity (off) to verify the resource is re-written unencrypted
func testKMSEncryptionKMSToKMSMigration(ctx context.Context, t testing.TB) {
	library.TestEncryptionProvidersMigration(ctx, t, library.ProvidersMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		CreateResourceFunc:             library.CreateAndStoreWellKnownSecretOfLife,
		AssertResourceEncryptedFunc:    library.AssertWellKnownSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: library.AssertWellKnownSecretOfLifeNotEncrypted,
		ResourceFunc:                   library.WellKnownSecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProviders: library.ShuffleEncryptionProviders([]library.EncryptionProvider{
			librarykms.DefaultVaultEncryptionProvider(ctx, t),
			librarykms.SecondaryVaultEncryptionProvider(ctx, t),
		}),
	})
}
