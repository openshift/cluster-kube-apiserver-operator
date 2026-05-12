package e2e_encryption_rotation

import (
	"flag"
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

var provider = flag.String("provider", "aescbc", "encryption provider used by the tests")

// TestEncryptionRotation first encrypts data then it forces a key
// rotation by setting the "encyrption.Reason" in the operator's configuration
// file
func TestEncryptionRotation(t *testing.T) {
	library.TestEncryptionRotation(t, library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc: operatorencryption.CreateAndStoreSecretOfLife,
		GetRawResourceFunc: operatorencryption.GetRawSecretOfLife,
		UnsupportedConfigFunc: func(raw []byte) error {
			return operatorencryption.UpdateUnsupportedConfig(t, raw)
		},
		EncryptionProvider: library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: configv1.EncryptionType(*provider)}},
	})
}

// TestEncryptionRotationDuringFirstMigration applies encryption (initial storage migration) and forces a key
// rotation while that first migration is still running. The cluster must converge to the last requested write key.
func TestEncryptionRotationDuringFirstMigration(t *testing.T) {
	library.TestEncryptionRotationDuringFirstMigration(t, library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:        operatorencryption.CreateAndStoreSecretOfLife,
		GetRawResourceFunc:        operatorencryption.GetRawSecretOfLife,
		GetOperatorConditionsFunc: operatorencryption.GetClusterOperatorConditions,
		UnsupportedConfigFunc: func(raw []byte) error {
			return operatorencryption.UpdateUnsupportedConfig(t, raw)
		},
		EncryptionProvider: library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: configv1.EncryptionType(*provider)}},
	})
}

// TestEncryptionRotationDuringOngoingRotation forces a second key rotation while the migration triggered by the
// first forced rotation is still running. The cluster must converge to the last requested write key.
func TestEncryptionRotationDuringOngoingRotation(t *testing.T) {
	library.TestEncryptionRotationDuringOngoingRotation(t, library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:        operatorencryption.CreateAndStoreSecretOfLife,
		GetRawResourceFunc:        operatorencryption.GetRawSecretOfLife,
		GetOperatorConditionsFunc: operatorencryption.GetClusterOperatorConditions,
		UnsupportedConfigFunc: func(raw []byte) error {
			return operatorencryption.UpdateUnsupportedConfig(t, raw)
		},
		EncryptionProvider: library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: configv1.EncryptionType(*provider)}},
	})
}
