package e2e

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
)

func TestEncryptionTypeAESCBC(t *testing.T) {
	encryption.TestEncryptionTypeAESCBC(t)
}
