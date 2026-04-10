package e2e

import (
	"testing"
)

// This test calls the shared test function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestEncryptionTypeAESCBC(t *testing.T) {
	testEncryptionTypeAESCBC(t)
}
