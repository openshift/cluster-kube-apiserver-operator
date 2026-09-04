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
	g.It("TestKMSEncryptionRotation [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionRotation(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionRotation encrypts SecretOfLife with Vault KMS, rotates the Vault transit
// key, waits for remote key re-migration to complete, and verifies the etcd payload changed.
func testKMSEncryptionRotation(ctx context.Context, t testing.TB) {
	library.TestEncryptionRotation(ctx, t, library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		CreateResourceFunc:          library.CreateAndStoreWellKnownSecretOfLife,
		GetRawResourceFunc:          library.GetRawWellKnownSecretOfLife,
		ForceRotationFunc:           librarykms.ForceVaultKeyRotation(),
		WaitForRotationCompleteFunc: library.WaitForKMSRemoteKeyRotationComplete(),
		EncryptionProvider:          librarykms.DefaultVaultEncryptionProvider(ctx, t),
	})
}
