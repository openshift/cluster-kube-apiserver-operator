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
		{name: "OnAESCBC", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "OffIdentity", testFunc: TestEncryptionTypeIdentity},
		{name: "OnAESCBCSecond", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "OffIdentitySecond", testFunc: TestEncryptionTypeIdentity},
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
	encryption.TestEncryptionTypeAESCBC(t)
	// TODO: take some samples and make sure that after rotation they look different
	// because a different key was used to encrypt data
	e := encryption.NewE(t)
	clientSet := encryption.GetClients(e)
	lastMigratedKeyMeta, err := encryption.GetLastKeyMeta(clientSet.Kube)
	require.NoError(e, err)
	require.NoError(e, encryption.ForceKeyRotation(e, clientSet.Operator, fmt.Sprintf("test-key-rotation-%s", rand.String(4))))
	encryption.WaitForNextMigratedKey(e, clientSet.Kube, lastMigratedKeyMeta)
	encryption.AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeAESCBC)
	// TODO: assert conditions - operator and encryption migration controller must report status as active not progressing, and not failing
}
