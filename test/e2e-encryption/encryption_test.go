package e2e_encryption

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/rand"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	e := encryption.NewE(t)
	clientSet := encryption.SetAndWaitForEncryptionType(e, configv1.EncryptionTypeIdentity)
	encryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeIdentity)
}

func TestEncryptionTypeUnset(t *testing.T) {
	e := encryption.NewE(t)
	clientSet := encryption.SetAndWaitForEncryptionType(e, "")
	encryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeIdentity)
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	scenarios := []struct {
		name     string
		testFunc func(*testing.T)
	}{
		{name: "CreateAndStoreSecretOfLife", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.CreateAndStoreSecretOfLife(e, encryption.GetClients(e))
		}},
		{name: "OnAESCBC", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "AssertSecretOfLifeEncrypted", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e))
		}},
		{name: "OffIdentity", testFunc: TestEncryptionTypeIdentity},
		{name: "AssertSecretOfLifeNotEncrypted", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeNotEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e))
		}},
		{name: "OnAESCBCSecond", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "AssertSecretOfLifeEncryptedSecond", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e))
		}},
		{name: "OffIdentitySecond", testFunc: TestEncryptionTypeIdentity},
		{name: "AssertSecretOfLifeNotEncryptedSecond", testFunc: func(t *testing.T) {
			e := encryption.NewE(t)
			encryption.AssertSecretOfLifeNotEncrypted(e, encryption.GetClients(e), encryption.SecretOfLife(e))
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
	// step 1: create the secret of life
	e := encryption.NewE(t)
	clientSet := encryption.GetClients(e)
	encryption.CreateAndStoreSecretOfLife(e, encryption.GetClients(e))

	// step 2: run encryption aescbc scenario
	encryption.TestEncryptionTypeAESCBC(t)

	// step 3: take samples
	rawEncryptedSecretOfLifeWithKey1 := encryption.GetRawSecretOfLife(e, clientSet)

	// step 4: force key rotation and wait for migration to complete
	lastMigratedKeyMeta, err := encryption.GetLastKeyMeta(clientSet.Kube)
	require.NoError(e, err)
	require.NoError(e, encryption.ForceKeyRotation(e, clientSet.Operator, fmt.Sprintf("test-key-rotation-%s", rand.String(4))))
	encryption.WaitForNextMigratedKey(e, clientSet.Kube, lastMigratedKeyMeta)
	encryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeAESCBC)

	// step 5: verify if the secret of life was encrypted with a different key (step 2 vs step 4)
	rawEncryptedSecretOfLifeWithKey2 := encryption.GetRawSecretOfLife(e, clientSet)
	if rawEncryptedSecretOfLifeWithKey1 == rawEncryptedSecretOfLifeWithKey2 {
		t.Errorf("expected the secret of life to has a differnt content after a key rotation,\ncontentBeforeRotation %s\ncontentAfterRotation %s", rawEncryptedSecretOfLifeWithKey1, rawEncryptedSecretOfLifeWithKey2)
	}

	// TODO: assert conditions - operator and encryption migration controller must report status as active not progressing, and not failing for all scenarios
	// TODO: assert encryption config (resources) for all scenarios
}
