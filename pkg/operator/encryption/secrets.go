package encryption

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

func findSecretForKey(key keyAndMode, secrets []*corev1.Secret, targetNamespace string) *corev1.Secret {
	if key == (keyAndMode{}) {
		return nil
	}

	for _, secret := range secrets {
		sKeyAndMode, _, ok := secretToKeyAndMode(secret, targetNamespace)
		if !ok {
			continue
		}
		if sKeyAndMode == key {
			return secret.DeepCopy()
		}
	}

	return nil
}

func findSecretForKeyWithClient(key keyAndMode, secretClient corev1client.SecretsGetter, encryptionSecretSelector metav1.ListOptions, targetNamespace string) (*corev1.Secret, error) {
	if key == (keyAndMode{}) {
		return nil, nil
	}

	encryptionSecretList, err := secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(encryptionSecretSelector)
	if err != nil {
		return nil, err
	}

	for _, secret := range encryptionSecretList.Items {
		sKeyAndMode, _, ok := secretToKeyAndMode(&secret, targetNamespace)
		if !ok {
			continue
		}
		if sKeyAndMode == key {
			return secret.DeepCopy(), nil
		}
	}

	return nil, nil
}

func secretToKeyAndMode(encryptionSecret *corev1.Secret, targetNamespace string) (keyAndMode, uint64, bool) {
	component := encryptionSecret.Labels[encryptionSecretComponent]
	keyData := encryptionSecret.Data[encryptionSecretKeyData]
	keyMode := mode(encryptionSecret.Annotations[encryptionSecretMode])

	keyID, validKeyID := secretToKeyID(encryptionSecret)

	key := keyAndMode{
		key: apiserverconfigv1.Key{
			// we use keyID as the name to limit the length of the field as it is used as a prefix for every value in etcd
			Name:   strconv.FormatUint(keyID, 10),
			Secret: base64.StdEncoding.EncodeToString(keyData),
		},
		mode: keyMode,
	}
	invalidKey := len(keyData) == 0 || !validKeyID || component != targetNamespace
	switch keyMode {
	case aescbc, secretbox, identity:
	default:
		invalidKey = true
	}

	return key, keyID, !invalidKey
}

func secretToKeyID(encryptionSecret *corev1.Secret) (uint64, bool) {
	// see format and ordering comment above encryptionSecretComponent near the top of this file
	lastIdx := strings.LastIndex(encryptionSecret.Name, "-")
	keyIDStr := encryptionSecret.Name[lastIdx+1:] // this can never overflow since str[-1+1:] is always valid
	keyID, keyIDErr := strconv.ParseUint(keyIDStr, 10, 0)
	invalidKeyID := lastIdx == -1 || keyIDErr != nil
	return keyID, !invalidKeyID
}

func getResourceConfigs(encryptionState groupResourcesState) []apiserverconfigv1.ResourceConfiguration {
	resourceConfigs := make([]apiserverconfigv1.ResourceConfiguration, 0, len(encryptionState))

	for gr, grKeys := range encryptionState {
		resourceConfigs = append(resourceConfigs, apiserverconfigv1.ResourceConfiguration{
			Resources: []string{gr.String()}, // we are forced to lose data here because this API is broken
			Providers: secretsToProviders(grKeys),
		})
	}

	// make sure our output is stable
	sort.Slice(resourceConfigs, func(i, j int) bool {
		return resourceConfigs[i].Resources[0] < resourceConfigs[j].Resources[0] // each resource has its own keys
	})

	return resourceConfigs
}

func secretsToKeyAndModes(grKeys keysState) groupResourceKeys {
	desired := groupResourceKeys{}

	desired.writeKey = grKeys.writeKey()

	// keys have a duplicate of the write key
	// or there is no write key

	// we know these are sorted with highest key ID first
	readKeys := grKeys.readKeys()
	for i := range readKeys {
		readKey := readKeys[i]

		readKeyIsWriteKey := desired.hasWriteKey() && readKey == desired.writeKey
		// if present, do not include a duplicate write key in the read key list
		if !readKeyIsWriteKey {
			desired.readKeys = append(desired.readKeys, readKey)
		}

		// TODO consider being smarter about read keys we prune to avoid some rollouts
	}

	return desired
}

// secretsToProviders maps the write and read secrets to the equivalent read and write keys.
// it primarily handles the conversion of keyAndMode to the appropriate provider config.
// the identity mode is transformed into a custom aesgcm provider that simply exists to
// curry the associated null key secret through the encryption state machine.
func secretsToProviders(grKeys keysState) []apiserverconfigv1.ProviderConfiguration {
	desired := secretsToKeyAndModes(grKeys)

	allKeys := desired.readKeys

	// write key comes first
	if desired.hasWriteKey() {
		allKeys = append([]keyAndMode{desired.writeKey}, allKeys...)
	}

	providers := make([]apiserverconfigv1.ProviderConfiguration, 0, len(allKeys)+1) // one extra for identity

	// having identity as a key is problematic because IdentityConfiguration cannot store any data.
	// we need to be able to trace back to the secret so that it can move through the key state machine.
	// thus in this case we create a fake AES-GCM config and include that at the very end of our providers.
	// its null key will never be used to encrypt data but it will be able to move through the observed states.
	// we guarantee it is never used by making sure that the IdentityConfiguration is always ahead of it.
	var hasIdentityAsWriteKey, needsFakeIdentityProvider bool
	var fakeIdentityProvider apiserverconfigv1.ProviderConfiguration

	for i, key := range allKeys {
		switch key.mode {
		case aescbc:
			providers = append(providers, apiserverconfigv1.ProviderConfiguration{
				AESCBC: &apiserverconfigv1.AESConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			})
		case secretbox:
			providers = append(providers, apiserverconfigv1.ProviderConfiguration{
				Secretbox: &apiserverconfigv1.SecretboxConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			})
		case identity:
			// we can only track one fake identity provider
			// this is not an issue because all identity providers are conceptually equivalent
			// because they all lead to the same outcome (read and write unencrypted data)
			if needsFakeIdentityProvider {
				continue
			}
			needsFakeIdentityProvider = true
			hasIdentityAsWriteKey = i == 0
			fakeIdentityProvider = apiserverconfigv1.ProviderConfiguration{
				AESGCM: &apiserverconfigv1.AESConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			}
		default:
			// this should never happen because our input should always be valid
			klog.Infof("skipping key %s as it has invalid mode %s", key.key.Name, key.mode)
		}
	}

	identityProvider := apiserverconfigv1.ProviderConfiguration{
		Identity: &apiserverconfigv1.IdentityConfiguration{},
	}

	if desired.hasWriteKey() && !hasIdentityAsWriteKey {
		// the common case is that we have a write key, identity comes last
		providers = append(providers, identityProvider)
	} else {
		// if we have no write key, identity comes first
		providers = append([]apiserverconfigv1.ProviderConfiguration{identityProvider}, providers...)
	}

	if needsFakeIdentityProvider {
		providers = append(providers, fakeIdentityProvider)
	}

	return providers
}
