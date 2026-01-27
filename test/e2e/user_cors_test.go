package e2e

import (
	"testing"
)

// This test calls the shared test functions which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestAdditionalCORSAllowedOrigins(t *testing.T) {
	t.Run("CORS with single additional origin", func(t *testing.T) {
		testCORSWithSingleOrigin(t)
	})

	t.Run("CORS with multiple additional origins", func(t *testing.T) {
		testCORSWithMultipleOrigins(t)
	})

	t.Run("CORS clearing to defaults", func(t *testing.T) {
		testCORSClearToDefaults(t)
	})
}
