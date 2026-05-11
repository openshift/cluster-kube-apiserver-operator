package encryptiondata

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/library-go/pkg/operator/encryption/encoding"
	"github.com/openshift/library-go/pkg/operator/encryption/kms"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
)

// EncryptionConfSecretName is the name of the final encryption config secret that is revisioned per apiserver rollout.
const EncryptionConfSecretName = "encryption-config"

// EncryptionConfSecretKey is the map data key used to store the raw bytes of the final encryption config.
const EncryptionConfSecretKey = "encryption-config"

func FromSecret(encryptionConfigSecret *corev1.Secret) (*Config, error) {
	data, ok := encryptionConfigSecret.Data[EncryptionConfSecretKey]
	if !ok {
		return nil, nil
	}
	encryptionConfig, err := encoding.DecodeEncryptionConfiguration(data)
	if err != nil {
		return nil, err
	}
	var kmsPlugins map[string]configv1.KMSPluginConfig
	for key, value := range encryptionConfigSecret.Data {
		// Not all data keys are plugin configs — the Secret also contains the
		// encryption-config entry, so skip keys that don't match the pattern.
		keyID, found, err := kms.KeyIDFromPluginConfigSecretDataKey(key)
		if err != nil {
			return nil, fmt.Errorf("failed to extract keyID from data key %s: %w", key, err)
		}
		if !found {
			continue
		}
		pluginConfig, err := encoding.DecodeKMSPluginConfig(value)
		if err != nil {
			return nil, fmt.Errorf("failed to decode KMS plugin config for key %s: %w", keyID, err)
		}
		if kmsPlugins == nil {
			kmsPlugins = map[string]configv1.KMSPluginConfig{}
		}
		kmsPlugins[keyID] = pluginConfig
	}

	return &Config{Encryption: encryptionConfig, KMSPlugins: kmsPlugins}, nil
}

func ToSecret(ns, name string, secretData *Config) (*corev1.Secret, error) {
	if !secretData.HasEncryptionConfiguration() {
		return nil, fmt.Errorf("secret %s/%s has no encryption config", ns, name)
	}

	rawEncryptionCfg, err := encoding.EncodeEncryptionConfiguration(secretData.Encryption)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the encryption config: %v", err)
	}

	s := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				state.KubernetesDescriptionKey: state.KubernetesDescriptionScaryValue,
			},
			Finalizers: []string{"encryption.apiserver.operator.openshift.io/deletion-protection"},
		},
		Data: map[string][]byte{
			EncryptionConfSecretName: rawEncryptionCfg,
		},
		Type: corev1.SecretTypeOpaque,
	}

	for keyID, pluginConfig := range secretData.KMSPlugins {
		encodedPlugin, err := encoding.EncodeKMSPluginConfig(pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to encode KMS plugin config for key %s: %w", keyID, err)
		}
		dataKey, err := kms.ToPluginConfigSecretDataKeyFor(keyID)
		if err != nil {
			return nil, err
		}
		s.Data[dataKey] = encodedPlugin
	}

	return s, nil
}
