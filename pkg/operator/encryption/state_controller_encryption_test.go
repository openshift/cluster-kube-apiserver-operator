package encryption

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestEncryptionStateController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialResources         []runtime.Object
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		// expectedActions holds actions to be verified in the form of "verb:resource"
		expectedActions []string
		// destName denotes the name of the secret that contains EncryptionConfiguration
		// this field is required to create the controller
		destName              string
		expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration
		validateFunc          func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration)
	}{
		// scenario 1: validates if "encryption-config-kube-apiserver-test" secret with EncryptionConfiguration in "openshift-config-managed" namespace
		// was not created when no secrets with encryption keys are present in that namespace.
		{
			name:            "no secret with EncryptionConfig is created when there are no secrets with the encryption keys",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			expectedActions: []string{"list:pods", "list:secrets"},
		},

		// scenario 2: validates if "encryption-config-kube-apiserver-test" secret with EncryptionConfiguration in "openshift-config-managed" namespace is created,
		// it also checks the content and the order of encryption providers, this test expects identity first and aescbc second
		{
			name:                     "secret with EncryptionConfig is created and it contains a read only key",
			targetNamespace:          "kms",
			encryptionSecretSelector: metav1.ListOptions{LabelSelector: "encryption.operator.openshift.io/component=kms"},
			destName:                 "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createSecretBuilder("kms", schema.GroupResource{"", "secrets"}, 1).
					withEncryptionKey([]byte("61def964fb967f5d7c44a2af8dab6865")).toCoreV1Secret(),
			},
			expectedActions:       []string{"list:pods", "list:secrets", "get:secrets", "create:secrets", "create:events"},
			expectedEncryptionCfg: createEncryptionCfgNoWriteKey("1", "NjFkZWY5NjRmYjk2N2Y1ZDdjNDRhMmFmOGRhYjY4NjU=", "secrets"),
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("create", "secrets") {
						createAction := action.(clientgotesting.CreateAction)
						actualSecret := createAction.GetObject().(*corev1.Secret)
						err := validateSecretWithEncryptionConfig(actualSecret, expectedEncryptionCfg, destName)
						if err != nil {
							ts.Fatalf("failed to verfy the encryption config, due to %v", err)
						}
						wasSecretValidated = true
						break
					}
				}
				if !wasSecretValidated {
					ts.Errorf("the secret wasn't created and validated")
				}
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// setup
			fakeOperatorClient := v1helpers.NewFakeStaticPodOperatorClient(
				&operatorv1.StaticPodOperatorSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: operatorv1.Managed,
					},
				},
				&operatorv1.StaticPodOperatorStatus{
					OperatorStatus: operatorv1.OperatorStatus{
						// we need to set up proper conditions before the test starts because
						// the controller calls UpdateStatus which calls UpdateOperatorStatus method which is unsupported (fake client) and throws an exception
						Conditions: []operatorv1.OperatorCondition{
							operatorv1.OperatorCondition{
								Type:   "EncryptionStateControllerDegraded",
								Status: "False",
							},
						},
					},
				},
				nil,
				nil,
			)
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialResources...)
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events("test"), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" and $targetNamespace ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed", scenario.targetNamespace)
			fakeSecretClient := fakeKubeClient.CoreV1()
			fakePodClient := fakeKubeClient.CoreV1().Pods(scenario.targetNamespace)

			target := newEncryptionStateController(
				scenario.targetNamespace, scenario.destName,
				fakeOperatorClient,
				kubeInformers,
				fakeSecretClient,
				scenario.encryptionSecretSelector,
				eventRecorder,
				scenario.targetGRs,
				fakePodClient,
			)

			// act
			err := target.sync()

			// validate
			if err != nil {
				t.Fatal(err)
			}
			if err := validateActionsVerbs(fakeKubeClient.Actions(), scenario.expectedActions); err != nil {
				t.Fatalf("incorrect action(s) detected: %v", err)
			}
			if scenario.validateFunc != nil {
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.destName, scenario.expectedEncryptionCfg)
			}
		})
	}
}

func validateSecretWithEncryptionConfig(actualSecret *corev1.Secret, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration, expectedSecretName string) error {
	actualEncryptionCfg, err := secretDataToEncryptionConfig(actualSecret)
	if err != nil {
		return fmt.Errorf("failed to verfy the encryption config, due to %v", err)
	}

	if !equality.Semantic.DeepEqual(actualEncryptionCfg, expectedEncryptionCfg) {
		return fmt.Errorf("%s", diff.ObjectDiff(actualEncryptionCfg, expectedEncryptionCfg))
	}

	// rewrite the payload and compare the rest
	expectedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      expectedSecretName,
			Namespace: "openshift-config-managed",
		},
		Data: actualSecret.Data,
	}
	if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
		return fmt.Errorf("%s", diff.ObjectDiff(actualSecret, expectedSecret))
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
