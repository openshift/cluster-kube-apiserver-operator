package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	//g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
	//	testKMSEncryptionOnOff(ctx, g.GinkgoTB())
	//})

	//g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
	//	testKMSEncryptionProvidersMigration(ctx, g.GinkgoTB())
	//})

	g.It("TestKMSToKMSOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSToKMSOnOff(ctx, g.GinkgoTB())
	})

	g.It("TestKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSToKMSMigration(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionOnOff tests KMS encryption on/off cycle.
// This test:
// 1. Deploys the real Vault KMS plugin
// 2. Creates a test secret (SecretOfLife)
// 3. Enables KMS encryption
// 4. Verifies secret is encrypted
// 5. Disables encryption (Identity)
// 6. Verifies secret is NOT encrypted
// 7. Re-enables KMS encryption
// 8. Verifies secret is encrypted again
// 9. Disables encryption (Identity) again
// 10. Verifies secret is NOT encrypted again
func testKMSEncryptionOnOff(ctx context.Context, t testing.TB) {
	library.TestEncryptionTurnOnAndOff(ctx, t, library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider:             librarykms.DefaultVaultEncryptionProvider(ctx, t),
	})
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES encryption providers.
// This test:
// 1. Deploys the real Vault KMS plugin
// 2. Creates a test secret (SecretOfLife)
// 3. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 4. Shuffles the selected AES provider with KMS to create a randomized migration order
// 5. Migrates between the providers in the shuffled order
// 6. Verifies secret is correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	library.TestEncryptionProvidersMigration(ctx, t, library.ProvidersMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProviders: library.ShuffleEncryptionProviders([]library.EncryptionProvider{
			librarykms.DefaultVaultEncryptionProvider(ctx, t),
			library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))],
		}),
	})
}

// testKMSToKMSMigration tests KMS-to-KMS migration (primary → secondary → primary → identity).
func testKMSToKMSMigration(ctx context.Context, t testing.TB) {
	library.TestKMSToKMSMigration(ctx, t, library.KMSToKMSMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		PrimaryProvider:                librarykms.DefaultVaultEncryptionProvider(ctx, t),
		SecondaryProvider:              librarykms.SecondaryVaultEncryptionProvider(ctx, t),
	})
}

// testKMSToKMSOnOff tests KMS on/off cycle with two distinct KMS providers.
func testKMSToKMSOnOff(ctx context.Context, t testing.TB) {
	library.TestKMSToKMSOnOff(ctx, t, library.KMSToKMSMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		PrimaryProvider:                librarykms.DefaultVaultEncryptionProvider(ctx, t),
		SecondaryProvider:              librarykms.SecondaryVaultEncryptionProvider(ctx, t),
	})
}
