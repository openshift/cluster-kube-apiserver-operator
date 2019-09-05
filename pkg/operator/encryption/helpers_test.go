package encryption

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	clientgotesting "k8s.io/client-go/testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	encryptionSecretKeyDataForTest           = "encryption.operator.openshift.io-key"
	encryptionConfSecretForTest              = "encryption-config"
	encryptionSecretReadTimestampForTest     = "encryption.operator.openshift.io/read-timestamp"
	encryptionSecretWriteTimestampForTest    = "encryption.operator.openshift.io/write-timestamp"
	encryptionSecretMigratedTimestampForTest = "encryption.operator.openshift.io/migrated-timestamp"
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
			Annotations: map[string]string{
				"encryption.operator.openshift.io/mode": "aescbc",
			},
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

func createReadEncryptionKeySecretWithRawKey(targetNS string, gr schema.GroupResource, keyID uint64, rawKey []byte, timestamp ...string) *corev1.Secret {
	secret := createEncryptionKeySecretWithRawKey(targetNS, gr, keyID, rawKey)
	formattedTS := ""
	if len(timestamp) == 0 {
		formattedTS = time.Now().Format(time.RFC3339)
	} else {
		formattedTS = timestamp[0]
	}
	secret.Annotations[encryptionSecretReadTimestampForTest] = formattedTS
	return secret
}

func createWriteEncryptionKeySecretWithRawKey(targetNS string, gr schema.GroupResource, keyID uint64, rawKey []byte, timestamp ...string) *corev1.Secret {
	secret := createReadEncryptionKeySecretWithRawKey(targetNS, gr, keyID, rawKey, timestamp...)
	formattedTS := ""
	if len(timestamp) == 0 {
		formattedTS = time.Now().Format(time.RFC3339)
	} else {
		formattedTS = timestamp[0]
	}
	secret.Annotations[encryptionSecretWriteTimestampForTest] = formattedTS
	return secret
}

func createMigratedEncryptionKeySecretWithRawKey(targetNS string, gr schema.GroupResource, keyID uint64, rawKey []byte, timestamp ...string) *corev1.Secret {
	secret := createWriteEncryptionKeySecretWithRawKey(targetNS, gr, keyID, rawKey, timestamp...)
	formattedTS := ""
	if len(timestamp) == 0 {
		formattedTS = time.Now().Format(time.RFC3339)
	} else {
		formattedTS = timestamp[0]
	}
	secret.Annotations[encryptionSecretMigratedTimestampForTest] = formattedTS
	return secret
}

func createExpiredMigratedEncryptionKeySecretWithRawKey(targetNS string, gr schema.GroupResource, keyID uint64, rawKey []byte) *corev1.Secret {
	return createMigratedEncryptionKeySecretWithRawKey(targetNS, gr, keyID, rawKey, time.Now().Add(time.Minute*35*-1).Format(time.RFC3339))
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

func createDummyKubeAPIPodInUnknownPhase(name, namespace string) *corev1.Pod {
	p := createDummyKubeAPIPod(name, namespace)
	p.Status.Phase = corev1.PodUnknown
	return p
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
		actualActionNs := actualAction.GetNamespace()

		expectedAction := expectedActions[index]
		expectedActionVerRes := strings.Split(expectedAction, ":")
		if len(expectedActionVerRes) != 3 {
			return fmt.Errorf("cannot verify the action %q at position %d because it has an incorrect format, must be a tuple \"verb:resource:namespace\"", expectedAction, index)
		}
		expectedActionVerb := expectedActionVerRes[0]
		expectedActionRes := expectedActionVerRes[1]
		expectedActionNs := expectedActionVerRes[2]

		if actualActionVerb != expectedActionVerb {
			return fmt.Errorf("expected %q verb at position %d but got %q, for %q action", expectedActionVerb, index, actualActionVerb, expectedAction)
		}
		if actualActionRes != expectedActionRes {
			return fmt.Errorf("expected %q resource at position %d but got %q, for %q action", expectedActionRes, index, actualActionRes, expectedAction)
		}
		if actualActionNs != expectedActionNs {
			return fmt.Errorf("expected %q namespace at position %d but got %q, for %q action", expectedActionNs, index, actualActionNs, expectedAction)
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

func createEncryptionCfgWithWriteKey(keysResources []encryptionKeysResourceTuple) *apiserverconfigv1.EncryptionConfiguration {
	configurations := []apiserverconfigv1.ResourceConfiguration{}
	for _, keysResource := range keysResources {
		// TODO allow secretbox -> not sure if encryptionKeysResourceTuple makes sense
		providers := []apiserverconfigv1.ProviderConfiguration{}
		for _, key := range keysResource.keys {
			providers = append(providers, apiserverconfigv1.ProviderConfiguration{
				AESCBC: &apiserverconfigv1.AESConfiguration{
					Keys: []apiserverconfigv1.Key{key},
				},
			})
		}
		providers = append(providers, apiserverconfigv1.ProviderConfiguration{
			Identity: &apiserverconfigv1.IdentityConfiguration{},
		})

		configurations = append(configurations, apiserverconfigv1.ResourceConfiguration{
			Resources: []string{keysResource.resource},
			Providers: providers,
		})
	}

	return &apiserverconfigv1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EncryptionConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1",
		},
		Resources: configurations,
	}
}

func createEncryptionCfgSecretWithWriteKeys(t *testing.T, targetNs string, revision string, keysResources []encryptionKeysResourceTuple) *corev1.Secret {
	t.Helper()

	encryptionCfg := createEncryptionCfgWithWriteKey(keysResources)
	rawEncryptionCfg, err := runtime.Encode(encoder, encryptionCfg)
	if err != nil {
		t.Fatalf("unable to encode the encryption config, err = %v", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", encryptionConfSecretForTest, revision),
			Namespace: targetNs,
		},
		Data: map[string][]byte{
			encryptionConfSecretForTest: rawEncryptionCfg,
		},
	}
}

type encryptionKeysResourceTuple struct {
	resource string
	keys     []apiserverconfigv1.Key
}

func validateOperatorClientConditions(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient, expectedConditions []operatorv1.OperatorCondition) {
	ts.Helper()
	_, status, _, err := operatorClient.GetStaticPodOperatorState()
	if err != nil {
		ts.Fatal(err)
	}

	if len(status.Conditions) != len(expectedConditions) {
		ts.Fatalf("expected to get %d conditions from operator client but got %d", len(expectedConditions), len(status.Conditions))
	}

	for _, actualCondition := range status.Conditions {
		actualConditionValidated := false
		for _, expectedCondition := range expectedConditions {
			expectedCondition.LastTransitionTime = actualCondition.LastTransitionTime
			if equality.Semantic.DeepEqual(expectedCondition, actualCondition) {
				actualConditionValidated = true
				break
			}
		}
		if !actualConditionValidated {
			ts.Fatalf("unexpected condition found %v", actualCondition)
		}

	}
}
