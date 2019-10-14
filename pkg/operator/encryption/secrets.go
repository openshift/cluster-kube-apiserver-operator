package encryption

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/klog"
)

func secretToKeyAndMode(s *corev1.Secret) (KeyState, error) {
	data := s.Data[encryptionSecretKeyData]

	keyID, validKeyID := nameToKeyID(s.Name)
	if !validKeyID {
		return KeyState{}, fmt.Errorf("secret %s/%s has an invalid name", s.Namespace, s.Name)
	}

	key := KeyState{
		key: apiserverconfigv1.Key{
			// we use keyID as the name to limit the length of the field as it is used as a prefix for every value in etcd
			Name:   strconv.FormatUint(keyID, 10),
			Secret: base64.StdEncoding.EncodeToString(data),
		},
		backed: true,
	}

	if v, ok := s.Annotations[encryptionSecretMigratedTimestamp]; ok {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return KeyState{}, fmt.Errorf("secret %s/%s has invalid %s annotation: %v", s.Namespace, s.Name, encryptionSecretMigratedTimestamp, err)
		}
		key.migrated.ts = ts
	}

	if v, ok := s.Annotations[encryptionSecretMigratedResources]; ok && len(v) > 0 {
		migrated := &migratedGroupResources{}
		if err := json.Unmarshal([]byte(v), migrated); err != nil {
			return KeyState{}, fmt.Errorf("secret %s/%s has invalid %s annotation: %v", s.Namespace, s.Name, encryptionSecretMigratedResources, err)
		}
		key.migrated.resources = migrated.Resources
	}

	if v, ok := s.Annotations[encryptionSecretInternalReason]; ok && len(v) > 0 {
		key.internalReason = v
	}
	if v, ok := s.Annotations[encryptionSecretExternalReason]; ok && len(v) > 0 {
		key.externalReason = v
	}

	keyMode := mode(s.Annotations[encryptionSecretMode])
	switch keyMode {
	case aescbc, secretbox, identity:
		key.mode = keyMode
	default:
		return KeyState{}, fmt.Errorf("secret %s/%s has invalid mode: %s", s.Namespace, s.Name, keyMode)
	}
	if keyMode != identity && len(data) == 0 {
		return KeyState{}, fmt.Errorf("secret %s/%s has of mode %q must have non-empty key", s.Namespace, s.Name, keyMode)
	}

	return key, nil
}

func nameToKeyID(name string) (uint64, bool) {
	lastIdx := strings.LastIndex(name, "-")
	keyIDStr := name[lastIdx+1:] // this can never overflow since str[-1+1:] is always valid
	keyID, keyIDErr := strconv.ParseUint(keyIDStr, 10, 0)
	invalidKeyID := lastIdx == -1 || keyIDErr != nil
	return keyID, !invalidKeyID
}

func getResourceConfigs(encryptionState map[schema.GroupResource]GroupResourceState) []apiserverconfigv1.ResourceConfiguration {
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

// secretsToProviders maps the write and read secrets to the equivalent read and write keys.
// it primarily handles the conversion of KeyState to the appropriate provider config.
// the identity mode is transformed into a custom aesgcm provider that simply exists to
// curry the associated null key secret through the encryption state machine.
func secretsToProviders(desired GroupResourceState) []apiserverconfigv1.ProviderConfiguration {
	allKeys := desired.readKeys

	// write key comes first
	if desired.hasWriteKey() {
		allKeys = append([]KeyState{desired.writeKey}, allKeys...)
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
