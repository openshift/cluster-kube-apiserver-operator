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
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestEncryptionKeyController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialSecrets           []runtime.Object
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		// expectedActions holds actions to be verified in the form of "verb:resource:namespace"
		expectedActions            []string
		validateFunc               func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool)
		validateOperatorClientFunc func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient)
		expectedError              error
	}{
		// scenario 1: assumes a clean slate, that is, there are no previous resources in the system.
		// It expects that a secret resource with an appropriate key, name and labels will be created.
		{
			name: "checks if a secret with AES256 key for core/secret is created",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed", "create:secrets:openshift-config-managed"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool) {
				var targetGR schema.GroupResource
				for targetGR = range targetGRs {
					break
				}
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("create", "secrets") {
						createAction := action.(clientgotesting.CreateAction)
						actualSecret := createAction.GetObject().(*corev1.Secret)
						expectedSecret := createEncryptionKeySecretWithKeyFromExistingSecret(targetNamespace, targetGR, 1, actualSecret)
						if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
							ts.Errorf(diff.ObjectDiff(actualSecret, expectedSecret))
						}
						if err := validateEncryptionKey(actualSecret); err != nil {
							ts.Error(err)
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

		// scenario 2: verifies if a new key is not created when there is a valid write key in the system.
		{
			name: "no-op when a valid write key exists",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 7, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 3: checks if a new key is not created when there is a valid write (migrated/used) key in the system.
		{
			name: "no-op when a valid migrated key exists",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 3, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 4: checks if a new write key is created because the previous one was migrated.
		{
			name: "creates a new write key because the previous one expired",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 5, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed", "create:secrets:openshift-config-managed"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool) {
				var targetGR schema.GroupResource
				for targetGR = range targetGRs {
					break
				}
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("create", "secrets") {
						createAction := action.(clientgotesting.CreateAction)
						actualSecret := createAction.GetObject().(*corev1.Secret)
						expectedSecret := createEncryptionKeySecretWithKeyFromExistingSecret(targetNamespace, targetGR, 6, actualSecret)
						expectedSecret.Annotations["encryption.operator.openshift.io/internal-reason"] = "timestamp-too-old"
						if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
							ts.Errorf(diff.ObjectDiff(actualSecret, expectedSecret))
						}
						if err := validateEncryptionKey(actualSecret); err != nil {
							ts.Error(err)
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

		// scenario 5: checks if a new write key is not created given that the previous one was migrated and the new write key already exists.
		{
			name: "no-op when the previous key was migrated and the current one is valid but hasn't been observed (no read/write annotations)",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 5, []byte("61def964fb967f5d7c44a2af8dab6865")),
				createEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 6, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 6: checks if a new secret write key with ID equal to "101" is created because the previous (with ID equal to "100") one was migrated.
		//             note that IDs of keys (not secrets) cannot exceed 100
		{
			name: "creates a new write key because the previous one expired - overflow",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 100, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed", "create:secrets:openshift-config-managed"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool) {
				var targetGR schema.GroupResource
				for targetGR = range targetGRs {
					break
				}
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("create", "secrets") {
						createAction := action.(clientgotesting.CreateAction)
						actualSecret := createAction.GetObject().(*corev1.Secret)
						expectedSecret := createEncryptionKeySecretWithKeyFromExistingSecret(targetNamespace, targetGR, 101, actualSecret)
						expectedSecret.Annotations["encryption.operator.openshift.io/internal-reason"] = "timestamp-too-old"
						if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
							ts.Errorf(diff.ObjectDiff(actualSecret, expectedSecret))
						}
						if err := validateEncryptionKey(actualSecret); err != nil {
							ts.Error(err)
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

		// scenario 7
		{
			name: "degraded a secret with invalid key exists",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: []runtime.Object{
				createEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 1, []byte("")),
			},
			targetNamespace: "kms",
			expectedActions: []string{"list:secrets:openshift-config-managed", "create:secrets:openshift-config-managed", "get:secrets:openshift-config-managed"},
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedCondition := operatorv1.OperatorCondition{
					Type:    "EncryptionKeyControllerDegraded",
					Status:  "True",
					Reason:  "Error",
					Message: "secret kms-core-secrets-encryption-1 is in invalid state, new keys cannot be created for encryption target group=core resource=secrets",
				}
				validateOperatorClientConditions(ts, operatorClient, []operatorv1.OperatorCondition{expectedCondition})
			},
			expectedError: errors.New("secret kms-core-secrets-encryption-1 is in invalid state, new keys cannot be created for encryption target group=core resource=secrets"),
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
								Type:   "EncryptionKeyControllerDegraded",
								Status: "False",
							},
						},
					},
				},
				nil,
				nil,
			)
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialSecrets...)
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events(scenario.targetNamespace), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed")
			fakeSecretClient := fakeKubeClient.CoreV1()
			target := newEncryptionKeyController(scenario.targetNamespace, fakeOperatorClient, kubeInformers, fakeSecretClient, scenario.encryptionSecretSelector, eventRecorder, scenario.targetGRs)

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
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.targetNamespace, scenario.targetGRs)
			}
			if scenario.validateOperatorClientFunc != nil {
				scenario.validateOperatorClientFunc(t, fakeOperatorClient)
			}
		})
	}
}

func validateEncryptionKey(secret *corev1.Secret) error {
	rawKey, exist := secret.Data[encryptionSecretKeyDataForTest]
	if !exist {
		return errors.New("the secret doesn't contain an encryption key")
	}
	if len(rawKey) != 32 {
		return fmt.Errorf("incorrect length of the encryption key, expected 32, got %d bytes", len(rawKey))
	}
	return nil
}
