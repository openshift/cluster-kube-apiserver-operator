package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"fmt"
)

// GenerateKeyPair generates a key pair based on the given KeyConfig.
func GenerateKeyPair(config KeyConfig) (crypto.PublicKey, crypto.PrivateKey, error) {
	switch config.Algorithm {
	case RSAKeyAlgorithm:
		bits := config.RSABits
		if bits == 0 {
			bits = keyBits
		}
		privateKey, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return nil, nil, err
		}
		return &privateKey.PublicKey, privateKey, nil
	case ECDSAKeyAlgorithm:
		curve, err := config.ellipticCurve()
		if err != nil {
			return nil, nil, err
		}
		privateKey, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		return &privateKey.PublicKey, privateKey, nil
	default:
		return nil, nil, fmt.Errorf("unsupported key algorithm: %q", config.Algorithm)
	}
}

// SubjectKeyIDFromPublicKey computes a SHA-1 hash suitable for use as
// a certificate SubjectKeyId from any supported public key type.
func SubjectKeyIDFromPublicKey(pub crypto.PublicKey) ([]byte, error) {
	var rawBytes []byte
	switch pub := pub.(type) {
	case *rsa.PublicKey:
		rawBytes = pub.N.Bytes()
	case *ecdsa.PublicKey:
		rawBytes = elliptic.Marshal(pub.Curve, pub.X, pub.Y)
	default:
		return nil, fmt.Errorf("unsupported public key type: %T", pub)
	}
	hash := sha1.New()
	hash.Write(rawBytes)
	return hash.Sum(nil), nil
}
