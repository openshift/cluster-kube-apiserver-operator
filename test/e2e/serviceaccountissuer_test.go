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
	t.Run("set a custom serviceAccountIssuer and expect it plus the default in kas config", func(t *testing.T) {
		testServiceAccountIssuerFirstIssuer(t)
	})

	t.Run("set a second custom issuer and expect both custom issuers plus the default in kas config", func(t *testing.T) {
		testServiceAccountIssuerSecondIssuer(t)
	})

	t.Run("clear serviceAccountIssuer and expect only the default issuer in kas config", func(t *testing.T) {
		testServiceAccountIssuerDefaultIssuer(t)
	})
}
