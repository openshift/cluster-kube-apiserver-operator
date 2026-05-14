package kms

import (
	configv1 "github.com/openshift/api/config/v1"
)

// DefaultFakeKMSPluginConfig is a fake Vault KMS configuration used by tests.
// The values are not real and are likely to change as the KMS integration evolves.
var DefaultFakeKMSPluginConfig = configv1.KMSPluginConfig{
	Type: configv1.VaultKMSProvider,
	Vault: configv1.VaultKMSPluginConfig{
		KMSPluginImage: WellKnownUpstreamMockKMSPluginImage,
		VaultAddress:   "https://vault.example.com",
		Authentication: configv1.VaultAuthentication{
			Type: configv1.VaultAuthenticationTypeAppRole,
			AppRole: configv1.VaultAppRoleAuthentication{
				Secret: configv1.VaultSecretReference{Name: "vault-approle-secret"},
			},
		},
		TransitKey: "test-transit-key",
	},
}
