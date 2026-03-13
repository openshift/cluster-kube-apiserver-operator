package e2e

import (
	"testing"
)

// TestTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
//
// This test calls the shared testTokenRequestAndReview function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestTokenRequestAndReview(t *testing.T) {
	testTokenRequestAndReview(t)
}

// TestBoundTokenOperandSecretDeletion verifies the operand secret is recreated after deletion.
//
// This test calls the shared testBoundTokenOperandSecretDeletion function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestBoundTokenOperandSecretDeletion(t *testing.T) {
	testBoundTokenOperandSecretDeletion(t)
}

// TestBoundTokenConfigMapDeletion verifies the configmap is recreated after deletion.
// Note: it will roll out a new version.
//
// This test calls the shared testBoundTokenConfigMapDeletion function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestBoundTokenConfigMapDeletion(t *testing.T) {
	testBoundTokenConfigMapDeletion(t)
}

// TestBoundTokenOperatorSecretDeletion verifies the operator secret is recreated
// with a new keypair after deletion. The configmap in the operand namespace should
// be updated immediately, and the secret once the configmap is present on all nodes.
// Note: it will roll out a new version.
//
// This test calls the shared testBoundTokenOperatorSecretDeletion function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestBoundTokenOperatorSecretDeletion(t *testing.T) {
	testBoundTokenOperatorSecretDeletion(t)
}
