package e2e

import "testing"

// This test calls the shared function which
// can be called from both standard Go tests and Ginkgo
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestCertRotationTimeUpgradeable(t *testing.T) {
	testCertRotationTimeUpgradeable(t)
}

// This test calls the shared function which
// can be called from both standard Go tests and Ginkgo
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestCertRotationStompOnBadType(t *testing.T) {
	testCertRotationStompOnBadType(t)
}
