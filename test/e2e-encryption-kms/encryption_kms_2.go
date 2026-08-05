package e2e_encryption_kms

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/encryption/kms/preflight"
	"github.com/openshift/library-go/pkg/operator/events"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})

	g.It("TestKMSPreflightDeploy [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSPreflightDeploy(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionImageUpdate [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionImageUpdate(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionKMSToKMSMigration tests migration between two distinct KMS providers
// (default Vault instance and secondary Vault instance).
// This test:
// 1. Shuffles the two KMS providers and one AES provider to create a randomized migration order
// 2. Migrates between the providers in the shuffled order
// 3. Verifies route is correctly encrypted after each migration
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
		CreateResourceFunc: library.CreateAndStoreWellKnownSecretOfLife,
		AssertResourceEncryptedFunc: func(t testing.TB, clientSet library.ClientSet, resource runtime.Object) {
			library.AssertWellKnownSecretOfLifeEncrypted(t, clientSet, resource)
			library.AssertWellKnownSecretOfLifeEncryptedWithKMS(t, clientSet,
				operatorclient.GlobalMachineSpecifiedConfigNamespace,
				"encryption.apiserver.operator.openshift.io/component="+operatorclient.TargetNamespace,
				resource)
		},
		AssertResourceNotEncryptedFunc: library.AssertWellKnownSecretOfLifeNotEncrypted,
		ResourceFunc:                   library.WellKnownSecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProviders: library.ShuffleEncryptionProviders([]library.EncryptionProvider{
			librarykms.DefaultVaultEncryptionProvider(ctx, t),
			librarykms.SecondaryVaultEncryptionProvider(ctx, t),
		}),
	})
}

func testKMSPreflightDeploy(ctx context.Context, t testing.TB) {
	library.TestPreflightDeployAndPodMatchesOperand(ctx, t, library.PreflightDeployScenario{
		BasicScenario: library.BasicScenario{
			// Preflight deploys into the operand namespace because the library-go
			// scenario validates the actual workload pod wiring there, unlike the
			// migration scenarios that operate on the rendered encryption config.
			Namespace:     operatorclient.TargetNamespace,
			LabelSelector: "apiserver=true",
		},
		CreateDeployerFunc: func(ctx context.Context, t testing.TB, cs library.ClientSet) *preflight.PodPreflightDeployer {
			image := library.OperatorImageFromDeployment(ctx, t,
				operatorclient.OperatorNamespace, "kube-apiserver-operator", "kube-apiserver-operator")
			recorder := events.NewInMemoryRecorder("kms-preflight-e2e", clock.RealClock{})
			return preflight.NewStaticPodPreflightDeployer(
				operatorclient.TargetNamespace, cs.Kube.CoreV1(), cs.Kube.RbacV1(),
				recorder, image, []string{"cluster-kube-apiserver-operator", "kms-preflight"}, library.PreflightDeployCallTimeout,
			)
		},
		CreateEncryptionConfigFunc: library.VaultPreflightEncryptionConfigSecret,
		AssertDeployFunc:           library.AssertPreflightDeploy,
		EncryptionProvider:         librarykms.DefaultVaultEncryptionProvider(ctx, t),
	})
}

// testKMSEncryptionImageUpdate tests that upgrading kmsPluginImage is an in-place
// change that does NOT create a new encryption key.
// This test:
// 1. Applies KMS encryption with the old Vault KMS plugin image
// 2. Upgrades kmsPluginImage to the newVault KMS plugin image
// 3. Verifies no new encryption key is created (in-place update)
// 4. Verifies the new image propagates to the KMS plugin pods
func testKMSEncryptionImageUpdate(ctx context.Context, t testing.TB) {
	realProvider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	realImage := realProvider.APIServerEncryption.KMS.Vault.KMSPluginImage

	// Initial provider uses the old image with real Vault connection config.
	initialImage := "quay.io/openshifttest/vault-kube-kms@sha256:4206f83742528de5a1cc0ff2b2e93476a1e44dc18caf595b6658d03603dcafa2"
	initialCfg := realProvider.APIServerEncryption
	initialCfg.KMS.Vault.KMSPluginImage = initialImage
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
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
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
