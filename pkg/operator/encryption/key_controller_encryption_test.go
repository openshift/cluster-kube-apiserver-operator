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

const (
	encryptionSecretKeyDataForTest = "encryption.operator.openshift.io-key"
)

func TestEncryptionKeyController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialSecrets           []runtime.Object
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		validateFunc             func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool)
	}{
		// scenario 1: assumes a clean slate, that is, there are no previous resources in the system.
		// It expects that a secret resource with an appropriate key, name and labels will be created.
		{
			name: "checks if a secret with AES256 key for core/secret is created",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}: true,
			},
			targetNamespace: "kms",
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, targetNamespace string, targetGRs map[schema.GroupResource]bool) {
				var targetGR schema.GroupResource
				for targetGR = range targetGRs {
					break
				}
				if len(actions) != 2 {
					ts.Fatalf("expected to get 2 actions but got %d", len(actions))
				}
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("create", "secrets") {
						createAction := action.(clientgotesting.CreateAction)
						actualSecret := createAction.GetObject().(*corev1.Secret)
						expectedSecret := createSecretFor(targetNamespace, targetGR, 1, actualSecret)
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
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events("test"), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed")
			fakeSecretClient := fakeKubeClient.CoreV1()
			target := newEncryptionKeyController(scenario.targetNamespace, fakeOperatorClient, kubeInformers, fakeSecretClient, scenario.encryptionSecretSelector, eventRecorder, scenario.targetGRs)

			// act
			err := target.sync()

			// validate
			if err != nil {
				t.Fatal(err)
			}
			if scenario.validateFunc != nil {
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.targetNamespace, scenario.targetGRs)
			}
		})
	}
}

func createSecretFor(targetNS string, gr schema.GroupResource, keyID uint64, actualSecret *corev1.Secret) *corev1.Secret {
	group := gr.Group
	if len(group) == 0 {
		group = "core"
	}

	data := map[string][]byte{}
	if rawKey, exist := actualSecret.Data[encryptionSecretKeyDataForTest]; exist {
		data[encryptionSecretKeyDataForTest] = rawKey
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
		Data: data,
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
