package encryption

import (
	"crypto/rand"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestEncryptionPruneController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialSecrets           []*corev1.Secret
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		// expectedActions holds actions to be verified in the form of "verb:resource:namespace"
		expectedActions       []string
		expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration
		validateFunc          func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret)
	}{
		// scenario 1
		{
			name:            "no-op only 10 keys were migrated",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: func() []*corev1.Secret {
				ns := "kms"
				all := []*corev1.Secret{}
				all = append(all, createMigratedEncryptionKeySecretsWithRndKey(t, 10, ns, "secrets")...)
				all = append(all, createWriteEncryptionKeySecretWithRawKey(ns, schema.GroupResource{"", "secrets"}, 11, []byte("cfbbae883984944e48d25590abdfd300")))
				return all
			}(),
			expectedActions: []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 2
		{
			name:            "11 keys were migrated so only the first key is pruned",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: func() []*corev1.Secret {
				return createMigratedEncryptionKeySecretsWithRndKey(t, 11, "kms", "secrets")
			}(),
			expectedActions: []string{"list:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "delete:secrets:openshift-config-managed"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret) {
				validateSecretsWerePruned(ts, actions, []*corev1.Secret{initialSecrets[0]})
			},
		},

		// scenario 3
		{
			name:            "no-op only 10 keys were migrated for each GR",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}:    true,
				schema.GroupResource{Group: "", Resource: "configmaps"}: true,
			},
			initialSecrets: func() []*corev1.Secret {
				ns := "kms"
				all := []*corev1.Secret{}
				all = append(all, createMigratedEncryptionKeySecretsWithRndKey(t, 10, ns, "secrets")...)
				all = append(all, createWriteEncryptionKeySecretWithRawKey(ns, schema.GroupResource{"", "secrets"}, 11, []byte("cfbbae883984944e48d25590abdfd300")))

				all = append(all, createMigratedEncryptionKeySecretsWithRndKey(t, 10, ns, "configmaps")...)
				all = append(all, createWriteEncryptionKeySecretWithRawKey(ns, schema.GroupResource{"", "configmaps"}, 11, []byte("aab0aebfdac418d55973b02e7fbd376f")))
				return all
			}(),
			expectedActions: []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 4
		{
			name:            "no-op the migrated keys don't match the selector",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			initialSecrets: func() []*corev1.Secret {
				return createMigratedEncryptionKeySecretsWithRndKey(t, 15, "not-kms", "secrets")
			}(),
			encryptionSecretSelector: metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", "encryption.operator.openshift.io/component", "kms")},
			expectedActions:          []string{"list:secrets:openshift-config-managed"},
		},

		// scenario 5
		{
			name:            "max 10 migrated keys are kept",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}:    true,
				schema.GroupResource{Group: "", Resource: "configmaps"}: true,
			},
			initialSecrets: func() []*corev1.Secret {
				ns := "kms"
				all := []*corev1.Secret{}
				all = append(all, createMigratedEncryptionKeySecretsWithRndKey(t, 21, ns, "secrets")...)
				all = append(all, createMigratedEncryptionKeySecretsWithRndKey(t, 21, ns, "configmaps")...)
				return all
			}(),
			// expectedActions are in the form of "verb:resource:namespace:resourcename" where resourcename is a regular expression
			// we use a regular expression because the order is important and the underlying client doesn't guarantee stable output
			expectedActions: []string{
				"list:secrets:openshift-config-managed",

				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-0", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-0",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-1", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-1",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-2", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-2",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-3", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-3",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-4", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-4",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-5", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-5",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-6", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-6",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-7", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-7",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-8", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-8",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-9", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-9",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-10", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-10",

				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-0", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-0",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-1", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-1",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-2", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-2",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-3", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-3",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-4", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-4",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-5", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-5",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-6", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-6",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-7", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-7",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-8", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-8",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-9", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-9",
				"update:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-10", "delete:secrets:openshift-config-managed:kms-core-(configmaps|secrets)-encryption-10",
			},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, initialSecrets []*corev1.Secret) {
				expectedPrunedKeys := []*corev1.Secret{}
				// initialSecrets holds 21 secrets and configmaps, we are interested only in the first 11 items of each as those will be removed
				expectedPrunedKeys = append(expectedPrunedKeys, initialSecrets[0:11]...)  // take the first 11 secrets
				expectedPrunedKeys = append(expectedPrunedKeys, initialSecrets[21:32]...) // take the first 11 configmaps
				validateSecretsWerePruned(ts, actions, expectedPrunedKeys)
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
						Conditions: []operatorv1.OperatorCondition{
							operatorv1.OperatorCondition{
								Type:   "EncryptionPruneControllerDegraded",
								Status: "False",
							},
						},
					},
				},
				nil,
				nil,
			)
			rawSecrets := []runtime.Object{}
			for _, initialSecret := range scenario.initialSecrets {
				rawSecrets = append(rawSecrets, initialSecret)
			}
			fakeKubeClient := fake.NewSimpleClientset(rawSecrets...)
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events(scenario.targetNamespace), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" and $targetNamespace ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed", scenario.targetNamespace)
			fakeSecretClient := fakeKubeClient.CoreV1()

			target := newEncryptionPruneController(
				scenario.targetNamespace,
				fakeOperatorClient,
				kubeInformers,
				fakeSecretClient,
				scenario.encryptionSecretSelector,
				eventRecorder,
				scenario.targetGRs,
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
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.initialSecrets)
			}
		})
	}
}

func validateSecretsWerePruned(ts *testing.T, actions []clientgotesting.Action, expectedDeletedSecrets []*corev1.Secret) {
	ts.Helper()

	expectedActionsNoList := len(actions) - 1 - len(expectedDeletedSecrets) // subtract "list" and "update" (removal of finalizer) requests
	if expectedActionsNoList != len(expectedDeletedSecrets) {
		ts.Fatalf("incorrect number of resources were pruned, expected %d, got %d", len(expectedDeletedSecrets), expectedActionsNoList)
	}

	deletedSecretsCount := 0
	finalizersRemovedCount := 0
	for _, action := range actions {
		if action.GetVerb() == "update" {
			updateAction := action.(clientgotesting.UpdateAction)
			actualSecret := updateAction.GetObject().(*corev1.Secret)
			for _, expectedDeletedSecret := range expectedDeletedSecrets {
				if expectedDeletedSecret.Name == actualSecret.GetName() {
					expectedDeletedSecretsCpy := expectedDeletedSecret.DeepCopy()
					expectedDeletedSecretsCpy.Finalizers = []string{}
					if equality.Semantic.DeepEqual(actualSecret, expectedDeletedSecretsCpy) {
						finalizersRemovedCount++
						break
					}
				}
			}
		}
		if action.GetVerb() == "delete" {
			deleteAction := action.(clientgotesting.DeleteAction)
			for _, expectedDeletedSecret := range expectedDeletedSecrets {
				if expectedDeletedSecret.Name == deleteAction.GetName() && expectedDeletedSecret.Namespace == deleteAction.GetNamespace() {
					deletedSecretsCount++
				}
			}
		}
	}
	if deletedSecretsCount != len(expectedDeletedSecrets) {
		ts.Errorf("%d key(s) were deleted but %d were expected to be deleted", deletedSecretsCount, len(expectedDeletedSecrets))
	}
	if finalizersRemovedCount != len(expectedDeletedSecrets) {
		ts.Errorf("expected to see %d finalizers removed but got %d", len(expectedDeletedSecrets), finalizersRemovedCount)
	}
}

func createMigratedEncryptionKeySecretsWithRndKey(ts *testing.T, count int, namespace, resource string) []*corev1.Secret {
	ts.Helper()
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		ts.Fatal(err)
	}
	ret := []*corev1.Secret{}
	for i := 0; i < count; i++ {
		s := createMigratedEncryptionKeySecretWithRawKey(namespace, schema.GroupResource{"", resource}, uint64(i), rawKey)
		ret = append(ret, s)
	}
	return ret
}
