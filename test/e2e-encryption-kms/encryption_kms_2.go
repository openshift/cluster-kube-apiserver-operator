package e2e_encryption_kms

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionKMSToKMSMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionKMSToKMSMigration(ctx, g.GinkgoTB())
	})
})

func testKMSEncryptionKMSToKMSMigration(ctx context.Context, t testing.TB) {
	providers := library.ShuffleEncryptionProviders([]library.EncryptionProvider{
		librarykms.DefaultVaultEncryptionProvider(ctx, t),
		librarykms.SecondaryVaultEncryptionProvider(ctx, t),
	})

	library.TestEncryptionProvidersMigration(ctx, t,
		library.ProvidersMigrationScenario{
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
			EncryptionProviders:            providers,
		},
		library.ProvidersMigrationScenario{
			BasicScenario: library.BasicScenario{
				Namespace:                       "openshift-config-managed",
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + "openshift-oauth-apiserver",
				EncryptionConfigSecretName:      "encryption-config-openshift-oauth-apiserver",
				EncryptionConfigSecretNamespace: "openshift-config-managed",
				OperatorNamespace:               "openshift-authentication-operator",
				TargetGRs:                       library.AuthTargetGRs,
				AssertFunc:                      library.AssertTokens,
			},
			CreateResourceFunc: func(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
				return library.CreateAndStoreTokenOfLife(context.TODO(), t, clientSet)
			},
			AssertResourceEncryptedFunc:    library.AssertTokenOfLifeEncrypted,
			AssertResourceNotEncryptedFunc: library.AssertTokenOfLifeNotEncrypted,
			ResourceFunc:                   library.TokenOfLife,
			ResourceName:                   "TokenOfLife",
			EncryptionProviders:            providers,
		},
	)
}
