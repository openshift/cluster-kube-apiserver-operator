package e2e_encryption_perf

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

// This test calls the shared TestPerfEncryption function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new encryption perf ote jobs.
// Eventually all tests will be run only as part of the OTE framework.
func TestPerfEncryption(tt *testing.T) {
	testPerfEncryption(tt, configv1.EncryptionType(*provider))
}
