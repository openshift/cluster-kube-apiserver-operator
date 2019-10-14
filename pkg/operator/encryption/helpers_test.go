package encryption

import (
	"encoding/json"
	"errors"
	"fmt"
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
	encryptionSecretKeyDataForTest           = "encryption.apiserver.operator.openshift.io-key"
	encryptionConfSecretForTest              = "encryption-config"
	encryptionSecretMigratedTimestampForTest = "encryption.apiserver.operator.openshift.io/migrated-timestamp"
	encryptionSecretMigratedResourcesForTest = "encryption.apiserver.operator.openshift.io/migrated-resources"
)

func createEncryptionKeySecretNoData(targetNS string, grs []schema.GroupResource, keyID uint64, mode ...string) *corev1.Secret {
	encryptionMode := ""
	if len(mode) > 1 {
		panic("only one mode is supported")
	}
	if len(mode) == 0 {
		encryptionMode = "aescbc"
	} else {
		encryptionMode = mode[0]
	}

	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-encryption-%d", targetNS, keyID),
			Namespace: "openshift-config-managed",
			Annotations: map[string]string{
				kubernetesDescriptionKey: kubernetesDescriptionScaryValue,

				"encryption.apiserver.operator.openshift.io/mode":            encryptionMode,
				"encryption.apiserver.operator.openshift.io/internal-reason": "no-secrets",
				"encryption.apiserver.operator.openshift.io/external-reason": "",
			},
			Labels: map[string]string{
				"encryption.apiserver.operator.openshift.io/component": targetNS,
			},
			Finalizers: []string{"encryption.apiserver.operator.openshift.io/deletion-protection"},
		},
		Data: map[string][]byte{},
	}

	if len(grs) > 0 {
		migratedResourceBytes, err := json.Marshal(migratedGroupResources{Resources: grs})
		if err != nil {
			panic(err)
		}
		s.Annotations[encryptionSecretMigratedResourcesForTest] = string(migratedResourceBytes)
	}

	return s
}

func createEncryptionKeySecretWithRawKey(targetNS string, grs []schema.GroupResource, keyID uint64, rawKey []byte, mode ...string) *corev1.Secret {
	secret := createEncryptionKeySecretNoData(targetNS, grs, keyID, mode...)
	secret.Data[encryptionSecretKeyDataForTest] = rawKey
	return secret
}

func createEncryptionKeySecretWithKeyFromExistingSecret(targetNS string, grs []schema.GroupResource, keyID uint64, existingSecret *corev1.Secret) *corev1.Secret {
	secret := createEncryptionKeySecretNoData(targetNS, grs, keyID)
	if rawKey, exist := existingSecret.Data[encryptionSecretKeyDataForTest]; exist {
		secret.Data[encryptionSecretKeyDataForTest] = rawKey
	}
	return secret
}

func createMigratedEncryptionKeySecretWithRawKey(targetNS string, grs []schema.GroupResource, keyID uint64, rawKey []byte, ts time.Time) *corev1.Secret {
	secret := createEncryptionKeySecretWithRawKey(targetNS, grs, keyID, rawKey)
	secret.Annotations[encryptionSecretMigratedTimestampForTest] = ts.Format(time.RFC3339)
	return secret
}

func createExpiredMigratedEncryptionKeySecretWithRawKey(targetNS string, grs []schema.GroupResource, keyID uint64, rawKey []byte) *corev1.Secret {
	return createMigratedEncryptionKeySecretWithRawKey(targetNS, grs, keyID, rawKey, time.Now().Add(-(time.Hour*24*7 + time.Hour)))
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
				{
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

	decoder := apiserverCodecs.UniversalDecoder(apiserverconfigv1.SchemeGroupVersion)
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
		return fmt.Errorf("expected to get %d actions but got %d, expected=%v, got=%v", len(expectedActions), len(actualActions), expectedActions, actionStrings(actualActions))
	}
	for i, a := range actualActions {
		if got, expected := actionString(a), expectedActions[i]; got != expected {
			return fmt.Errorf("at %d got %s, expected %s", i, got, expected)
		}
	}
	return nil
}

func actionString(a clientgotesting.Action) string {
	return a.GetVerb() + ":" + a.GetResource().Resource + ":" + a.GetNamespace()
}

func actionStrings(actions []clientgotesting.Action) []string {
	res := make([]string, 0, len(actions))
	for _, a := range actions {
		res = append(res, actionString(a))
	}
	return res
}

func createEncryptionCfgNoWriteKey(keyID string, keyBase64 string, resources ...string) *apiserverconfigv1.EncryptionConfiguration {
	keysResources := []keysResourceModes{}
	for _, resource := range resources {
		keysResources = append(keysResources, keysResourceModes{
			resource: resource,
			keys: []apiserverconfigv1.Key{
				{Name: keyID, Secret: keyBase64},
			},
		})

	}
	return createEncryptionCfgNoWriteKeyMultipleReadKeys(keysResources)
}

func createEncryptionCfgNoWriteKeyMultipleReadKeys(keysResources []keysResourceModes) *apiserverconfigv1.EncryptionConfiguration {
	ec := &apiserverconfigv1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "EncryptionConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1",
		},
		Resources: []apiserverconfigv1.ResourceConfiguration{},
	}

	for _, keysResource := range keysResources {

		rc := apiserverconfigv1.ResourceConfiguration{
			Resources: []string{keysResource.resource},
			Providers: []apiserverconfigv1.ProviderConfiguration{
				{
					Identity: &apiserverconfigv1.IdentityConfiguration{},
				},
			},
		}
		for index, key := range keysResource.keys {
			desiredMode := ""
			if len(keysResource.modes) == len(keysResource.keys) {
				desiredMode = keysResource.modes[index]
			}
			switch desiredMode {
			case "aesgcm":
				rc.Providers = append(rc.Providers, apiserverconfigv1.ProviderConfiguration{
					AESGCM: &apiserverconfigv1.AESConfiguration{
						Keys: []apiserverconfigv1.Key{key},
					},
				})
			default:
				rc.Providers = append(rc.Providers, apiserverconfigv1.ProviderConfiguration{
					AESCBC: &apiserverconfigv1.AESConfiguration{
						Keys: []apiserverconfigv1.Key{key},
					},
				})
			}
		}
		ec.Resources = append(ec.Resources, rc)
	}

	return ec
}

func createEncryptionCfgWithWriteKey(keysResources []keysResourceModes) *apiserverconfigv1.EncryptionConfiguration {
	configurations := []apiserverconfigv1.ResourceConfiguration{}
	for _, keysResource := range keysResources {
		// TODO allow secretbox -> not sure if keysResourceModes makes sense
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

func createEncryptionCfgSecret(t *testing.T, targetNs string, revision string, encryptionCfg *apiserverconfigv1.EncryptionConfiguration) *corev1.Secret {
	t.Helper()

	encoder := apiserverCodecs.LegacyCodec(apiserverconfigv1.SchemeGroupVersion)
	rawEncryptionCfg, err := runtime.Encode(encoder, encryptionCfg)
	if err != nil {
		t.Fatalf("unable to encode the encryption config, err = %v", err)
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", encryptionConfSecretForTest, revision),
			Namespace: targetNs,
			Annotations: map[string]string{
				kubernetesDescriptionKey: kubernetesDescriptionScaryValue,
			},
			Finalizers: []string{"encryption.apiserver.operator.openshift.io/deletion-protection"},
		},
		Data: map[string][]byte{
			encryptionConfSecretForTest: rawEncryptionCfg,
		},
	}
}

type keysResourceModes struct {
	resource string
	keys     []apiserverconfigv1.Key
	// an ordered list of an encryption modes thatch matches the keys
	// for example mode[0] matches keys[0]
	modes []string
}

func validateOperatorClientConditions(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient, expectedConditions []operatorv1.OperatorCondition) {
	ts.Helper()
	_, status, _, err := operatorClient.GetStaticPodOperatorState()
	if err != nil {
		ts.Fatal(err)
	}

	if len(status.Conditions) != len(expectedConditions) {
		ts.Fatalf("expected to get %d conditions from operator client but got %d:\n\nexpected=%v\n\ngot=%v", len(expectedConditions), len(status.Conditions), expectedConditions, status.Conditions)
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

func newFakeIdentityEncodedKeyForTest() string {
	return "AAAAAAAAAAAAAAAAAAAAAA=="
}

func newFakeIdentityKeyForTest() []byte {
	return make([]byte, 16)
}
