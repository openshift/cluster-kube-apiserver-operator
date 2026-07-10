package e2e_encryption

import (
	"flag"
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	library "github.com/openshift/library-go/test/library/encryption"
)

var provider = flag.String("provider", "aescbc", "encryption provider used by the tests")

func TestEncryptionTypeIdentity(t *testing.T) {
	library.TestEncryptionTypeIdentity(t.Context(), t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func TestEncryptionTypeUnset(t *testing.T) {
	library.TestEncryptionTypeUnset(t.Context(), t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	library.TestEncryptionTurnOnAndOff(t.Context(), t, library.OnOffScenario{
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
		EncryptionProvider:             library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: configv1.EncryptionType(*provider)}},
	})
}
