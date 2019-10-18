package e2e_encryption

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	e := encryption.NewE(t)
	etcdClient := encryption.TestEncryptionType(e, configv1.EncryptionTypeIdentity)
	encryption.AssertSecretsAndConfigMaps(e, etcdClient, string(configv1.EncryptionTypeIdentity))
}

func TestEncryptionTypeUnset(t *testing.T) {
	e := encryption.NewE(t)
	etcdClient := encryption.TestEncryptionType(e, "")
	encryption.AssertSecretsAndConfigMaps(e, etcdClient, string(configv1.EncryptionTypeIdentity))
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	scenarios := []struct {
		name     string
		testFunc func(*testing.T)
	}{
		{name: "OnAESCBC", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "OffIdentity", testFunc: TestEncryptionTypeIdentity},
		{name: "OnAESCBCSecond", testFunc: encryption.TestEncryptionTypeAESCBC},
		{name: "OnIdentitySecond", testFunc: TestEncryptionTypeIdentity},
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
