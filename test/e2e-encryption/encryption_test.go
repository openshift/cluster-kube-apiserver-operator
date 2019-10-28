package e2e_encryption

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	"github.com/openshift/library-go/test/library/encryption"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	e := encryption.NewE(t)
	ns := operatorclient.GlobalMachineSpecifiedConfigNamespace
	labelSelector := "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace
	clientSet := encryption.SetAndWaitForEncryptionType(e, configv1.EncryptionTypeIdentity, operatorencryption.DefaultTargetGRs, ns, labelSelector)
	operatorencryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeIdentity, ns, labelSelector)
}

func TestEncryptionTypeUnset(t *testing.T) {
	e := encryption.NewE(t)
	ns := operatorclient.GlobalMachineSpecifiedConfigNamespace
	labelSelector := "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace
	clientSet := encryption.SetAndWaitForEncryptionType(e, "", operatorencryption.DefaultTargetGRs, ns, labelSelector)
	operatorencryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeIdentity, ns, labelSelector)
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	scenarios := []struct {
		name     string
		testFunc func(*testing.T)
	}{
		{name: "CreateAndStoreSecretOfLife", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.CreateAndStoreSecretOfLife(e, encryption.GetClients(e), operatorclient.GlobalMachineSpecifiedConfigNamespace)
		}},
		{name: "OnAESCBC", testFunc: operatorencryption.TestEncryptionTypeAESCBC},
		{name: "AssertSecretOfLifeEncrypted", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e, operatorclient.GlobalMachineSpecifiedConfigNamespace))
		}},
		{name: "OffIdentity", testFunc: TestEncryptionTypeIdentity},
		{name: "AssertSecretOfLifeNotEncrypted", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeNotEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e, operatorclient.GlobalMachineSpecifiedConfigNamespace))
		}},
		{name: "OnAESCBCSecond", testFunc: operatorencryption.TestEncryptionTypeAESCBC},
		{name: "AssertSecretOfLifeEncryptedSecond", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e, operatorclient.GlobalMachineSpecifiedConfigNamespace))
		}},
		{name: "OffIdentitySecond", testFunc: TestEncryptionTypeIdentity},
		{name: "AssertSecretOfLifeNotEncryptedSecond", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeNotEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e, operatorclient.GlobalMachineSpecifiedConfigNamespace))
		}},
	}

	// run scenarios
	for _, testScenario := range scenarios {
		t.Run(testScenario.name, testScenario.testFunc)
		if t.Failed() {
			t.Errorf("stopping the test as %q scenario failed", testScenario.name)
			return
		}
	}
}

// TestEncryptionRotation first encrypts data with aescbc key
// then it forces a key rotation by setting the "encyrption.Reason" in the operator's configuration file
func TestEncryptionRotation(t *testing.T) {
	// TODO: dump events, conditions in case of an failure for all scenarios

	// test data
	ns := operatorclient.GlobalMachineSpecifiedConfigNamespace
	labelSelector := "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace

	// step 1: create the secret of life
	e := encryption.NewE(t)
	clientSet := encryption.GetClients(e)
	encryption.CreateAndStoreSecretOfLife(e, encryption.GetClients(e), ns)

	// step 2: run encryption aescbc scenario
	operatorencryption.TestEncryptionTypeAESCBC(t)

	// step 3: take samples
	rawEncryptedSecretOfLifeWithKey1 := encryption.GetRawSecretOfLife(e, clientSet, ns)

	// step 4: force key rotation and wait for migration to complete
	lastMigratedKeyMeta, err := encryption.GetLastKeyMeta(clientSet.Kube, ns, labelSelector)
	require.NoError(e, err)
	require.NoError(e, encryption.ForceKeyRotation(e, func(raw []byte) error {
		operatorClient := operatorencryption.GetOperator(t)
		apiServerOperator, err := operatorClient.Get("cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}
		apiServerOperator.Spec.UnsupportedConfigOverrides.Raw = raw
		_, err = operatorClient.Update(apiServerOperator)
		return err
	}, fmt.Sprintf("test-key-rotation-%s", rand.String(4))))
	encryption.WaitForNextMigratedKey(e, clientSet.Kube, lastMigratedKeyMeta, operatorencryption.DefaultTargetGRs, ns, labelSelector)
	operatorencryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeAESCBC, ns, labelSelector)

	// step 5: verify if the secret of life was encrypted with a different key (step 2 vs step 4)
	rawEncryptedSecretOfLifeWithKey2 := encryption.GetRawSecretOfLife(e, clientSet, ns)
	if rawEncryptedSecretOfLifeWithKey1 == rawEncryptedSecretOfLifeWithKey2 {
		t.Errorf("expected the secret of life to has a differnt content after a key rotation,\ncontentBeforeRotation %s\ncontentAfterRotation %s", rawEncryptedSecretOfLifeWithKey1, rawEncryptedSecretOfLifeWithKey2)
	}

	// TODO: assert conditions - operator and encryption migration controller must report status as active not progressing, and not failing for all scenarios
	// TODO: assert encryption config (resources) for all scenarios
}
