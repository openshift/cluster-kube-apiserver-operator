package certrotationcontroller

import (
	"testing"
)

func TestDefaultCertRotation(t *testing.T) {
	defaultFactorGot, err := parseCertRotationAccelerator()

	if err != nil {
		t.Errorf("expected no error, but got: %v", err)
	}
	if defaultFactorGot != 0 {
		t.Errorf("expected default cert rotation factor to be: %d, but got: %d", 0, defaultFactorGot)
	}
}
