package e2e_encryption

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestEncryptionTypeIdentity [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTypeIdentity(ctx, g.GinkgoTB())
	})
	g.It("TestEncryptionTypeUnset [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTypeUnset(ctx, g.GinkgoTB())
	})
	g.It("TestEncryptionTurnOnAndOff [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTurnOnAndOff(ctx, g.GinkgoTB())
	})
})

func testEncryptionTypeIdentity(ctx context.Context, t testing.TB) {
	library.TestEncryptionTypeIdentity(ctx, t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func testEncryptionTypeUnset(ctx context.Context, t testing.TB) {
	library.TestEncryptionTypeUnset(ctx, t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func testEncryptionTurnOnAndOff(ctx context.Context, t testing.TB) {
	encType := operatorencryption.EncryptionTypeFromEnv(t)
	t.Logf("encryption type: %s\n", encType)
	library.TestEncryptionTurnOnAndOff(ctx, t, library.OnOffScenario{
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
		EncryptionProvider:             library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: encType}},
	})
}
