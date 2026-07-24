package e2e_encryption_kms

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		AssertDeployFunc: func(ctx context.Context, t testing.TB, cs library.ClientSet, namespace string, deployer *preflight.PodPreflightDeployer) {
			library.AssertPreflightDeploy(ctx, t, cs, namespace, deployer)
			pod, err := cs.Kube.CoreV1().Pods(namespace).Get(ctx, preflight.PodName, metav1.GetOptions{})
			require.NoError(t, err)
			require.True(t, pod.Spec.HostNetwork, "static-pod preflight should use hostNetwork")
		},
		EncryptionProvider: librarykms.DefaultVaultEncryptionProvider(ctx, t),
	})
}
