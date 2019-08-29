package encryption

import (
	"errors"
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

func TestEncryptionPodStateController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialResources         []runtime.Object
		initialSecrets           []*corev1.Secret
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		// expectedActions holds actions to be verified in the form of "verb:resource:namespace"
		expectedActions []string
		// destName denotes the name of the secret that contains EncryptionConfiguration
		// this field is required to create the controller
		destName                   string
		expectedEncryptionCfg      *apiserverconfigv1.EncryptionConfiguration
		validateFunc               func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret)
		validateOperatorClientFunc func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient)
		expectedError              error
	}{
		// scenario 1: checks if the controller reads encryption-config secret with an EncryptionConfiguration and if it finds and marks containing secret key as a read only key
		{
			name:            "verifies if a secret with an encryption key is marked as observed as a read key",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			initialSecrets: []*corev1.Secret{
				createEncryptionKeySecretWithRawKey("kms", schema.GroupResource{"", "secrets"}, 1, []byte("61def964fb967f5d7c44a2af8dab6865")),
				createNoWriteKeyEncryptionCfgSecret(t, "kms", "1", "1", "NjFkZWY5NjRmYjk2N2Y1ZDdjNDRhMmFmOGRhYjY4NjU=", "secrets"),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("update", "secrets") {
						updateAction := action.(clientgotesting.UpdateAction)
						actualSecret := updateAction.GetObject().(*corev1.Secret)

						// this test assumes that the encryption key secret is annotated
						// thus for simplicity, we rewrite the annotation and compare the rest
						expectedSecret := initialSecrets[0]
						if expectedSecret.Annotations == nil {
							expectedSecret.Annotations = map[string]string{}
						}
						expectedSecret.Annotations[encryptionSecretReadTimestampForTest] = actualSecret.Annotations[encryptionSecretReadTimestampForTest]

						if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
							ts.Errorf(diff.ObjectDiff(actualSecret, expectedSecret))
						}
						wasSecretValidated = true
						break
					}
				}
				if !wasSecretValidated {
					ts.Errorf("the secret wasn't updated and validated")
				}
			},
		},

		// scenario 2
		{
			name:            "verifies if a read key in the EncryptionConfig is marked as a write key",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			initialSecrets: []*corev1.Secret{
				createReadEncryptionKeySecretWithRawKey("kms", schema.GroupResource{"", "secrets"}, 1, []byte("71ea7c91419a68fd1224f88d50316b4e")),
				func() *corev1.Secret {
					keysRes := encryptionKeysResourceTuple{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							apiserverconfigv1.Key{
								Name:   "1",
								Secret: "NzFlYTdjOTE0MTlhNjhmZDEyMjRmODhkNTAzMTZiNGU=",
							},
						},
					}
					ec := createEncryptionCfgSecretWithWriteKeys(t, "kms", "1", []encryptionKeysResourceTuple{keysRes})
					return ec
				}(),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("update", "secrets") {
						updateAction := action.(clientgotesting.UpdateAction)
						actualSecret := updateAction.GetObject().(*corev1.Secret)

						// this test assumes that the encryption key secret is annotated
						// thus for simplicity, we rewrite the annotation and compare the rest
						expectedSecret := initialSecrets[0]
						if expectedSecret.Annotations == nil {
							expectedSecret.Annotations = map[string]string{}
						}
						expectedSecret.Annotations[encryptionSecretWriteTimestampForTest] = actualSecret.Annotations[encryptionSecretWriteTimestampForTest]

						if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
							ts.Errorf(diff.ObjectDiff(actualSecret, expectedSecret))
						}
						wasSecretValidated = true
						break
					}
				}
				if !wasSecretValidated {
					ts.Errorf("the secret wasn't updated and validated")
				}
			},
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedConditions := []operatorv1.OperatorCondition{
					operatorv1.OperatorCondition{
						Type:   "EncryptionPodStateControllerDegraded",
						Status: "False",
					},
					operatorv1.OperatorCondition{
						Type: "EncryptionPodStateControllerProgressing",
						// TODO: PodState controller never flips this to true
						Status: "False",
					},
				}
				validateOperatorClientConditions(ts, operatorClient, expectedConditions)
			},
		},

		// scenario 3
		{
			name:            "no-op when the EncryptionConfig contains the keys that have already been observed",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			initialSecrets: []*corev1.Secret{
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{"", "secrets"}, 0, []byte("237a8a4846c6b1890b12abf78e0db5a3")),
				createMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{"", "secrets"}, 1, []byte("71ea7c91419a68fd1224f88d50316b4e")),
				func() *corev1.Secret {
					keysRes := encryptionKeysResourceTuple{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							apiserverconfigv1.Key{
								Name:   "1",
								Secret: "NzFlYTdjOTE0MTlhNjhmZDEyMjRmODhkNTAzMTZiNGU=",
							},
							apiserverconfigv1.Key{
								Name:   "0",
								Secret: "MjM3YThhNDg0NmM2YjE4OTBiMTJhYmY3OGUwZGI1YTM=",
							},
						},
					}
					ec := createEncryptionCfgSecretWithWriteKeys(t, "kms", "1", []encryptionKeysResourceTuple{keysRes})
					return ec
				}(),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed"},
		},

		// scenario 4
		{
			name:            "degraded no encryption config ",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms"},
			expectedError:   errors.New("secrets \"encryption-config-1\" not found"),
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedConditions := []operatorv1.OperatorCondition{
					operatorv1.OperatorCondition{
						Type:    "EncryptionPodStateControllerDegraded",
						Status:  "True",
						Reason:  "Error",
						Message: "secrets \"encryption-config-1\" not found",
					},
					operatorv1.OperatorCondition{
						Type:   "EncryptionPodStateControllerProgressing",
						Status: "False",
					},
				}
				validateOperatorClientConditions(ts, operatorClient, expectedConditions)
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
								Type:   "EncryptionPodStateControllerDegraded",
								Status: "False",
							},
							operatorv1.OperatorCondition{
								Type:   "EncryptionPodStateControllerProgressing",
								Status: operatorv1.ConditionFalse,
							},
						},
					},
				},
				nil,
				nil,
			)
			for _, initialSecret := range scenario.initialSecrets {
				scenario.initialResources = append(scenario.initialResources, initialSecret)
			}
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialResources...)
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events(scenario.targetNamespace), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" and $targetNamespace ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed", scenario.targetNamespace)
			fakeSecretClient := fakeKubeClient.CoreV1()
			fakePodClient := fakeKubeClient.CoreV1().Pods(scenario.targetNamespace)

			target := newEncryptionPodStateController(
				scenario.targetNamespace,
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
			if err == nil && scenario.expectedError != nil {
				t.Fatal("expected to get an error from sync() method")
			}
			if err != nil && scenario.expectedError == nil {
				t.Fatal(err)
			}
			if err != nil && scenario.expectedError != nil && err.Error() != scenario.expectedError.Error() {
				t.Fatalf("unexpected error returned = %v, expected = %v", err, scenario.expectedError)
			}
			if err := validateActionsVerbs(fakeKubeClient.Actions(), scenario.expectedActions); err != nil {
				t.Fatalf("incorrect action(s) detected: %v", err)
			}
			if scenario.validateFunc != nil {
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.initialSecrets)
			}
			if scenario.validateOperatorClientFunc != nil {
				scenario.validateOperatorClientFunc(t, fakeOperatorClient)
			}
		})
	}
}

func createNoWriteKeyEncryptionCfgSecret(t *testing.T, targetNs, revision, keyID, keyBase64, keyResources string) *corev1.Secret {
	t.Helper()

	encryptionCfg := createEncryptionCfgNoWriteKey(keyID, keyBase64, keyResources)
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
