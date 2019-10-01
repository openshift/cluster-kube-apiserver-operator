package encryption

import (
	"crypto/rand"
	"encoding/base64"
)

var (
	modeToNewKeyFunc = map[mode]func() []byte{
		aescbc:    newAES256Key,
		secretbox: newAES256Key, // secretbox requires a 32 byte key so we can reuse the same function here
		identity:  newIdentityKey,
	}

	emptyStaticIdentityKey = base64.StdEncoding.EncodeToString(newIdentityKey())
)

func newAES256Key() []byte {
	b := make([]byte, 32) // AES-256 == 32 byte key
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func newIdentityKey() []byte {
	return make([]byte, 16) // the key is not used to perform encryption but must be a valid AES key
}
