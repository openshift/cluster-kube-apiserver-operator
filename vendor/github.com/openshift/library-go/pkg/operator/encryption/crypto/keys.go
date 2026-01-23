package crypto

import (
	"crypto/md5"
	"crypto/rand"

	"github.com/openshift/library-go/pkg/operator/encryption/state"
)

var (
	ModeToNewKeyFunc = map[state.Mode]func(externalKey []byte) []byte{
		state.AESCBC:    NewAES256Key,
		state.AESGCM:    NewAES256Key,
		state.SecretBox: NewAES256Key, // secretbox requires a 32 byte key so we can reuse the same function here
		state.Identity:  NewIdentityKey,
		state.KMS:       NewKMSKey,
	}
)

func NewAES256Key(_ []byte) []byte {
	b := make([]byte, 32) // AES-256 == 32 byte key
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func NewIdentityKey(_ []byte) []byte {
	return make([]byte, 16) // the key is not used to perform encryption but must be a valid AES key
}

func NewKMSKey(externalKey []byte) []byte {
	// Calculate MD5 checksum of KMS config for identification.
	// MD5 provides short fixed length to be used in EncryptionConfiguration provider name
	// e.g. kms-secrets-1-XUFAKrxLKna5cZnZEQH8Ug==
	hash := md5.Sum(externalKey)
	checksum := hash[:]
	return checksum
}
