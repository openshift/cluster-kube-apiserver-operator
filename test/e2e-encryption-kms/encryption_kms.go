package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionOnOff(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionProvidersMigration(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionInvalidImageRecovery [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionInvalidImageRecovery(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionOnOff tests KMS encryption on/off cycle.
// This test:
// 1. Deploys the mock KMS plugin
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
	// Deploy the mock KMS plugin for testing.
	// NOTE: This manual deployment is only required for KMS v1. In the future,
	// the platform will manage the KMS plugins, and this code will no longer be needed.
	librarykms.DeployUpstreamMockKMSPlugin(ctx, t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)
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
		EncryptionProvider:             librarykms.DefaultFakeVaultEncryptionProvider,
	})
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES encryption providers.
// This test:
// 1. Deploys the mock KMS plugin
// 2. Creates a test secret (SecretOfLife)
// 3. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 4. Shuffles the selected AES provider with KMS to create a randomized migration order
// 5. Migrates between the providers in the shuffled order
// 6. Verifies secret is correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	librarykms.DeployUpstreamMockKMSPlugin(ctx, t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)
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
			librarykms.DefaultFakeVaultEncryptionProvider,
			library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))],
		}),
	})
}

// testKMSEncryptionInvalidImageRecovery tests that an invalid KMS plugin image
// causes degradation and that fixing the image restores the cluster.
func testKMSEncryptionInvalidImageRecovery(ctx context.Context, t testing.TB) {
	librarykms.DeployUpstreamMockKMSPlugin(ctx, t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)

	invalidProvider := librarykms.DefaultFakeVaultEncryptionProvider
	invalidProvider.KMS.Vault.KMSPluginImage = "quay.io/openshift/invalid-kms-image:does-not-exist"

	library.TestEncryptionInvalidImageRecovery(ctx, t, library.InvalidImageRecoveryScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		InvalidImageProvider: invalidProvider,
		ValidImageProvider:   librarykms.DefaultFakeVaultEncryptionProvider,
		WaitForDegraded: func(ctx context.Context, t testing.TB) {
			t.Helper()
			operatorClient := operatorencryption.GetOperator(t)
			err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
				operator, err := operatorClient.Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				for _, cond := range operator.Status.Conditions {
					if cond.Type == "Degraded" && cond.Status == operatorv1.ConditionTrue {
						t.Logf("Operator is degraded: %s", cond.Message)
						return true, nil
					}
				}
				return false, nil
			})
			require.NoError(t, err, "timed out waiting for operator to become degraded")
		},
		WaitForRecovery: func(ctx context.Context, t testing.TB) {
			t.Helper()
			operatorClient := operatorencryption.GetOperator(t)
			err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
				operator, err := operatorClient.Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				degraded := false
				progressing := false
				for _, cond := range operator.Status.Conditions {
					if cond.Type == "Degraded" && cond.Status == operatorv1.ConditionTrue {
						degraded = true
					}
					if cond.Type == "Progressing" && cond.Status == operatorv1.ConditionTrue {
						progressing = true
					}
				}
				if !degraded && !progressing {
					t.Log("Operator recovered: not degraded and not progressing")
					return true, nil
				}
				return false, nil
			})
			require.NoError(t, err, "timed out waiting for operator to recover")
		},
	})
}
