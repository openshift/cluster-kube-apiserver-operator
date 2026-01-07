package e2e

import (
	"testing"
)

// This test calls the shared  function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestServiceAccountIssuerFirstIssuer(t *testing.T) {
	testServiceAccountIssuerFirstIssuer(t)
}

// This test calls the shared  function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestServiceAccountIssuerSecondIssuer(t *testing.T) {
	testServiceAccountIssuerSecondIssuer(t)
}

// This test calls the shared  function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestServiceAccountIssuerDefaultIssuer(t *testing.T) {
	testServiceAccountIssuerDefaultIssuer(t)
}
