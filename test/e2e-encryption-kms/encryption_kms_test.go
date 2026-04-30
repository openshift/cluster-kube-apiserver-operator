package e2e_encryption_kms

import (
	"testing"
)

// This test calls the shared test function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-encryption-kms-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestKMSEncryptionOnOff(t *testing.T) {
	testKMSEncryptionOnOff(t)
}

// This test calls the shared test function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-encryption-kms-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestKMSEncryptionProvidersMigration(t *testing.T) {
	testKMSEncryptionProvidersMigration(t)
}
