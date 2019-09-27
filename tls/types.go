package tls

import (
	"time"

	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

type Secret struct {
	Namespace, Name string
}

type ConfigMap struct {
	Namespace, Name string
}

type InputConfigMap struct {
	ConfigMap
	ProvidedBy string
}

type InputSecret struct {
	Secret
	ProvidedBy string
}

type RotatedCertificate struct {
	Secret
	Validity, Refresh time.Duration
	Signer            SignerConfig
	CABundle          CABundleConfig
}

type SignerConfig struct {
	Namespace, Name   string
	Validity, Refresh time.Duration
}

type CABundleConfig struct {
	Namespace, Name string
}

type RotatedSigner struct {
	RotatedCertificates []*RotatedCertificate
}

type RotatedCABundle struct {
	RotatedCertificates []*RotatedCertificate
}

type SyncedSecret struct {
	Secret
	From SecretReference
}

type SyncedConfigMap struct {
	ConfigMap
	From ConfigMapReference
}

type CombinedCABundle struct {
	ConfigMap
	From []ConfigMapReference
}

func (b *CombinedCABundle) FromResourceLocations() []resourcesynccontroller.ResourceLocation {
	ret := make([]resourcesynccontroller.ResourceLocation, 0, len(b.From))
	for _, c := range b.From {
		ret = append(ret, c.ToConfigMap().ResourceLocation())
	}
	return ret
}

type SecretReference interface {
	ToSecret() *Secret
}

type ConfigMapReference interface {
	ToConfigMap() *ConfigMap
}

func (s *Secret) ToSecret() *Secret {
	return s
}

func (c *ConfigMap) ToConfigMap() *ConfigMap {
	return c
}

func (s *Secret) ResourceLocation() resourcesynccontroller.ResourceLocation {
	return resourcesynccontroller.ResourceLocation{Namespace: s.Namespace, Name: s.Name}
}

func (c *ConfigMap) ResourceLocation() resourcesynccontroller.ResourceLocation {
	return resourcesynccontroller.ResourceLocation{Namespace: c.Namespace, Name: c.Name}
}

func (b *RotatedCABundle) ToConfigMap() *ConfigMap {
	// TODO: find clever way to enforce that s.RotatedCertificates is non-empty and complete
	return &ConfigMap{Namespace: b.RotatedCertificates[0].Namespace, Name: b.RotatedCertificates[0].Name}
}

func (s *RotatedSigner) ToSecret() *Secret {
	// TODO: find clever way to enforce that s.RotatedCertificates is non-empty and complete
	return &Secret{Namespace: s.RotatedCertificates[0].Namespace, Name: s.RotatedCertificates[0].Name}
}
