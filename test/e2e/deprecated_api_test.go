package e2e

import (
	"testing"
)

// This tests calls the shared test functions which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestAPIRemovedInNextReleaseInUse(t *testing.T) {
	testAPIRemovedInNextReleaseInUse(t)
}

// This tests calls the shared test functions which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestAPIRemovedInNextEUSReleaseInUse(t *testing.T) {
	testAPIRemovedInNextEUSReleaseInUse(t)
}
