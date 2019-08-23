package encryption

import (
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	clientgotesting "k8s.io/client-go/testing"
)

const (
	encryptionSecretKeyDataForTest       = "encryption.operator.openshift.io-key"
	encryptionConfSecretForTest          = "encryption-config"
	encryptionSecretReadTimestampForTest = "encryption.operator.openshift.io/read-timestamp"
)

func createEncryptionKeySecretNoData(targetNS string, gr schema.GroupResource, keyID uint64) *corev1.Secret {
	group := gr.Group
	if len(group) == 0 {
		group = "core"
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s-encryption-%d", targetNS, group, gr.Resource, keyID),
			Namespace: "openshift-config-managed",
			Labels: map[string]string{
				"encryption.operator.openshift.io/component": targetNS,
				"encryption.operator.openshift.io/group":     gr.Group,
				"encryption.operator.openshift.io/resource":  gr.Resource,
			},
		},
		Data: map[string][]byte{},
	}
}

func createEncryptionKeySecretWithRawKey(targetNS string, gr schema.GroupResource, keyID uint64, rawKey []byte) *corev1.Secret {
	secret := createEncryptionKeySecretNoData(targetNS, gr, keyID)
	secret.Data[encryptionSecretKeyDataForTest] = rawKey
	return secret
}

func createEncryptionKeySecretWithKeyFromExistingSecret(targetNS string, gr schema.GroupResource, keyID uint64, existingSecret *corev1.Secret) *corev1.Secret {
	secret := createEncryptionKeySecretNoData(targetNS, gr, keyID)
	if rawKey, exist := existingSecret.Data[encryptionSecretKeyDataForTest]; exist {
		secret.Data[encryptionSecretKeyDataForTest] = rawKey
	}
	return secret
}

func createDummyKubeAPIPod(name, namespace string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"apiserver": "true",
				"revision":  "1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				corev1.PodCondition{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

func secretDataToEncryptionConfig(secret *corev1.Secret) (*apiserverconfigv1.EncryptionConfiguration, error) {
	rawEncryptionConfig, exist := secret.Data[encryptionConfSecretForTest]
	if !exist {
		return nil, errors.New("the secret doesn't contain an encryption configuration")
	}

	decodedEncryptionConfig, err := runtime.Decode(decoder, rawEncryptionConfig)
	if err != nil {
		return nil, err
	}

	encryptionConfig, ok := decodedEncryptionConfig.(*apiserverconfigv1.EncryptionConfiguration)
	if !ok {
		return nil, fmt.Errorf("encryption config has wrong type %T", decodedEncryptionConfig)
	}
	return encryptionConfig, nil
}

func validateActionsVerbs(actualActions []clientgotesting.Action, expectedActions []string) error {
	if len(actualActions) != len(expectedActions) {
		return fmt.Errorf("expected to get %d actions but got %d", len(expectedActions), len(actualActions))
	}
	for index, actualAction := range actualActions {
		actualActionVerb := actualAction.GetVerb()
		actualActionRes := actualAction.GetResource().Resource

		expectedAction := expectedActions[index]
		expectedActionVerRes := strings.Split(expectedAction, ":")
		if len(expectedActionVerRes) != 2 {
			return fmt.Errorf("cannot verify the action %q at position %d because it has an incorrect format, must be a tuple \"verb:resource\"", expectedAction, index)
		}
		expectedActionVerb := expectedActionVerRes[0]
		expectedActionRes := expectedActionVerRes[1]

		if actualActionVerb != expectedActionVerb {
			return fmt.Errorf("expected %q verb at position %d but got %q, for %q action", expectedActionVerb, index, actualActionVerb, expectedAction)
		}
		if actualActionRes != expectedActionRes {
			return fmt.Errorf("expected %q resource at position %d but got %q, for %q action", expectedActionRes, index, actualActionRes, expectedAction)
		}
	}
	return nil
}

func createEncryptionCfgNoWriteKey(keyID string, keyBase64 string, resources ...string) *apiserverconfigv1.EncryptionConfiguration {
	return &apiserverconfigv1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EncryptionConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1",
		},
		Resources: []apiserverconfigv1.ResourceConfiguration{
			apiserverconfigv1.ResourceConfiguration{
				Resources: resources,
				Providers: []apiserverconfigv1.ProviderConfiguration{
					apiserverconfigv1.ProviderConfiguration{
						Identity: &apiserverconfigv1.IdentityConfiguration{},
					},
					apiserverconfigv1.ProviderConfiguration{
						AESCBC: &apiserverconfigv1.AESConfiguration{
							Keys: []apiserverconfigv1.Key{
								apiserverconfigv1.Key{Name: keyID, Secret: keyBase64},
							},
						},
					},
				},
			},
		},
	}
}
