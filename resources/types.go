package resources

import "time"

// TODO: these go to library-go

type Object struct {
	Namespace, Name string
}

type ConfigMap struct {
	Object
}

type CertConfigMap struct {
	ConfigMap
}

type Secret struct {
	Object
}

type KeyCertSecret struct {
	Secret
}

type RotatedKeyCertSecret struct {
	KeyCertSecret

	SigningKey     *KeyCertSecret
	PublicCABundle *CABundle

	CAValidity          time.Duration
	CARefreshPercentage float64

	CertValidity          time.Duration
	CertRefreshPercentage float64
}

type CABundle struct {
	ConfigMap
}
