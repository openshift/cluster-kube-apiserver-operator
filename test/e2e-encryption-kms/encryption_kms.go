package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	libraryapiserver "github.com/openshift/library-go/test/library/apiserver"
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

	g.It("TestKMSEncryptionInPlaceImageUpdate [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionInPlaceImageUpdate(ctx, g.GinkgoTB())
	})

	// Run last: it intentionally leaves the cluster in a degraded state mid-test and
	// must fully recover before subsequent jobs reuse the cluster.
	g.It("TestKMSEncryptionInvalidImageRecovery [OCPFeatureGate:KMSEncryption][Serial][Timeout:200m]", func(ctx context.Context) {
		testKMSEncryptionInvalidImageRecovery(ctx, g.GinkgoTB())
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

// testKMSEncryptionInvalidImageRecovery tests recovery from an invalid KMS plugin image:
// 1. Applies a KMS config with a non-existent plugin image
// 2. Waits for pods to enter ImagePullBackOff
// 3. Switches to AESCBC and verifies no new encryption key is created (revisions stuck)
// 4. Applies the valid KMS config and verifies recovery and successful encryption
func testKMSEncryptionInvalidImageRecovery(ctx context.Context, t testing.TB) {
	validProvider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	invalidEncryption := validProvider.APIServerEncryption
	// Only swap the plugin image. DefaultFakeKMSPluginConfig uses vault.example.com,
	// a different transit key, and no TLS — those are migration-triggering fields, so
	// recovery on the existing key never completes.
	invalidEncryption.KMS.Vault.KMSPluginImage = "quay.io/openshift/invalid-kms-image@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	invalidImageProvider := library.EncryptionProvider{
		APIServerEncryption: invalidEncryption,
		Setup:               validProvider.Setup,
	}

	library.TestKMSInvalidEncryptionRecovery(ctx, t, library.KMSInvalidEncryptionRecoveryScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		InvalidProvider: invalidImageProvider,
		ValidProvider:   validProvider,
		WaitForStuck: func(ctx context.Context, t testing.TB) {
			cs := library.GetClients(t)
			library.WaitForPodImagePullBackOff(ctx, t, cs.Kube, operatorclient.TargetNamespace, "", 10*time.Minute)
		},
	})
	cs := library.GetClients(t)
	podClient := cs.Kube.CoreV1().Pods(operatorclient.TargetNamespace)
	if err := libraryapiserver.WaitForAPIServerToStabilizeOnTheSameRevision(t, podClient); err != nil {
		t.Fatalf("apiserver pods did not stabilize after recovery: %v", err)
	}
}

// testKMSEncryptionInPlaceImageUpdate tests that updating kmsPluginImage in-place
// takes effect without creating a new encryption key:
//  1. Applies valid Vault KMS config with the real plugin image
//  2. Updates kmsPluginImage to the mock plugin image
//  3. Verifies no new encryption key is created and the updated image is running
func testKMSEncryptionInPlaceImageUpdate(ctx context.Context, t testing.TB) {
	initialProvider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	updatedEncryption := initialProvider.APIServerEncryption
	updatedEncryption.KMS.Vault.KMSPluginImage = librarykms.DefaultFakeKMSPluginConfig.KMS.Vault.KMSPluginImage
	updatedProvider := library.EncryptionProvider{
		APIServerEncryption: updatedEncryption,
		Setup:               initialProvider.Setup,
	}
	expectedImage := librarykms.DefaultFakeKMSPluginConfig.KMS.Vault.KMSPluginImage

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
		UpdatedProvider: updatedProvider,
		WaitForPropagation: func(ctx context.Context, t testing.TB, keyMeta library.EncryptionKeyMeta) {
			cs := library.GetClients(t)
			library.WaitForPodContainerCondition(ctx, t, cs.Kube, operatorclient.TargetNamespace, "", keyMeta.Name,
				func(pod corev1.Pod, keyName string) bool {
					keyID := keyName[strings.LastIndex(keyName, "-")+1:]
					pluginName := "vault-kms-plugin-" + keyID
					for _, c := range pod.Spec.InitContainers {
						if c.Name != pluginName || c.Image != expectedImage {
							continue
						}
						for _, status := range pod.Status.InitContainerStatuses {
							if status.Name != pluginName {
								continue
							}
							return status.Ready && status.State.Running != nil
						}
						return false
					}
					return false
				})
		},
	})
}
