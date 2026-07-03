package e2e_encryption_kms

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionImageUpdate [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionImageUpdate(ctx, g.GinkgoTB())
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
			librarykms.SecondaryVaultEncryptionProvider(ctx, t),
		}),
	})
}

// testKMSEncryptionImageUpdate tests that upgrading kmsPluginImage is an in-place
// change that does NOT create a new encryption key.
// This test:
// 1. Applies KMS encryption with the mock KMS plugin image
// 2. Upgrades kmsPluginImage to the real Vault KMS plugin image
// 3. Verifies no new encryption key is created (in-place update)
// 4. Verifies the real image propagates to the KMS plugin pods
func testKMSEncryptionImageUpdate(ctx context.Context, t testing.TB) {
	realProvider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	realImage := realProvider.APIServerEncryption.KMS.Vault.KMSPluginImage

	// Initial provider uses the mock image with real Vault connection config.
	mockImage := librarykms.DefaultFakeKMSPluginConfig.KMS.Vault.KMSPluginImage
	initialCfg := realProvider.APIServerEncryption
	initialCfg.KMS.Vault.KMSPluginImage = mockImage
	initialProvider := library.EncryptionProvider{
		APIServerEncryption: initialCfg,
		Setup:               realProvider.Setup,
	}

	library.TestKMSInPlaceUpdate(ctx, t, library.KMSInPlaceUpdateScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		Provider:        initialProvider,
		UpdatedProvider: realProvider,
		WaitForPropagation: func(ctx context.Context, t testing.TB, keyMeta library.EncryptionKeyMeta) {
			cs := library.GetClients(t)
			library.WaitForPodContainerCondition(ctx, t, cs.Kube,
				operatorclient.TargetNamespace,
				"encryption.apiserver.operator.openshift.io/component="+operatorclient.TargetNamespace,
				keyMeta.Name,
				func(pod corev1.Pod, _ string) bool {
					for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
						if c.Image == realImage {
							return true
						}
					}
					return false
				},
			)
		},
	})
}
