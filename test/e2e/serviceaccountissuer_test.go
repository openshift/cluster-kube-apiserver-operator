package e2e

import (
	"testing"
)

// This test calls the shared functions which can be called from both
// standard Go tests and Ginkgo tests. This test runs all three phases
// sequentially to verify the service account issuer lifecycle:
// 1. Setting first issuer
// 2. Setting second issuer (verifies first is retained as trusted)
// 3. Resetting to default issuer
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestServiceAccountIssuer(t *testing.T) {
	t.Run("serviceaccountissuer set in authentication config results in apiserver config", func(t *testing.T) {
		testServiceAccountIssuerFirstIssuer(t)
	})

	t.Run("second serviceaccountissuer set in authentication config results in apiserver config with two issuers", func(t *testing.T) {
		testServiceAccountIssuerSecondIssuer(t)
	})

	t.Run("no serviceaccountissuer set in authentication config results in apiserver config with default issuer set", func(t *testing.T) {
		testServiceAccountIssuerDefaultIssuer(t)
	})
}
