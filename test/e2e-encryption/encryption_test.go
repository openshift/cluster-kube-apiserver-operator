package e2e_encryption

import (
	"testing"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	testEncryptionTypeIdentity(t.Context(), t)
}

func TestEncryptionTypeUnset(t *testing.T) {
	testEncryptionTypeUnset(t.Context(), t)
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	testEncryptionTurnOnAndOff(t.Context(), t)
}
