package encryption

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/client-go/discovery"
	dynamicfakeclient "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	openapi_v2 "github.com/googleapis/gnostic/OpenAPIv2"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestEncryptionMigrationController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialResources         []runtime.Object
		initialSecrets           []*corev1.Secret
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                map[schema.GroupResource]bool
		targetAPIResources       []metav1.APIResource
		// expectedActions holds actions to be verified in the form of "verb:resource:namespace"
		expectedActions            []string
		validateFunc               func(ts *testing.T, actionsKube []clientgotesting.Action, actionsDynamic []clientgotesting.Action, initialSecrets []*corev1.Secret, targetGRs map[schema.GroupResource]bool, unstructuredObjs []runtime.Object)
		validateOperatorClientFunc func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient)
		expectedError              error
	}{
		// scenario 1
		{
			name:            "a happy path scenario that tests resources encryption and secrets annotation",
			targetNamespace: "kms",
			targetGRs: map[schema.GroupResource]bool{
				schema.GroupResource{Group: "", Resource: "secrets"}:    true,
				schema.GroupResource{Group: "", Resource: "configmaps"}: true,
			},
			targetAPIResources: []metav1.APIResource{
				metav1.APIResource{
					Name:       "secrets",
					Namespaced: true,
					Group:      "",
					Version:    "v1",
				},
				metav1.APIResource{
					Name:       "configmaps",
					Namespaced: true,
					Group:      "",
					Version:    "v1",
				},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				func() runtime.Object {
					cm := createConfigMap("cm-1", "os")
					cm.Kind = "ConfigMap"
					cm.APIVersion = corev1.SchemeGroupVersion.String()
					return cm
				}(),
				func() runtime.Object {
					cm := createConfigMap("cm-2", "os")
					cm.Kind = "ConfigMap"
					cm.APIVersion = corev1.SchemeGroupVersion.String()
					return cm
				}(),
			},
			initialSecrets: []*corev1.Secret{
				func() *corev1.Secret {
					s := createWriteEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "secrets"}, 1, []byte("71ea7c91419a68fd1224f88d50316b4e"))
					s.Kind = "Secret"
					s.APIVersion = corev1.SchemeGroupVersion.String()
					return s
				}(),
				func() *corev1.Secret {
					s := createWriteEncryptionKeySecretWithRawKey("kms", schema.GroupResource{Group: "", Resource: "configmaps"}, 1, []byte("7d00bfbed09a2f4113e61413511a2af3"))
					s.Kind = "Secret"
					s.APIVersion = corev1.SchemeGroupVersion.String()
					return s
				}(),
				func() *corev1.Secret {
					keysResForSecrets := encryptionKeysResourceTuple{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							apiserverconfigv1.Key{
								Name:   "1",
								Secret: "NzFlYTdjOTE0MTlhNjhmZDEyMjRmODhkNTAzMTZiNGU=",
							},
						},
					}
					keysResForConfigMaps := encryptionKeysResourceTuple{
						resource: "configmaps",
						keys: []apiserverconfigv1.Key{
							apiserverconfigv1.Key{
								Name:   "1",
								Secret: "N2QwMGJmYmVkMDlhMmY0MTEzZTYxNDEzNTExYTJhZjM=",
							},
						},
					}
					ec := createEncryptionCfgSecretWithWriteKeys(t, "kms", "1", []encryptionKeysResourceTuple{keysResForConfigMaps, keysResForSecrets})
					ec.Kind = "Secret"
					ec.APIVersion = corev1.SchemeGroupVersion.String()
					return ec
				}(),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actionsKube []clientgotesting.Action, actionsDynamic []clientgotesting.Action, initialSecrets []*corev1.Secret, targetGRs map[schema.GroupResource]bool, unstructuredObjs []runtime.Object) {
				// validate if the secrets were properly annotated
				validateSecretsWereAnnotated(ts, actionsKube, []*corev1.Secret{initialSecrets[0], initialSecrets[1]})
				// validate if the resources were "encrypted"
				validateMigratedResources(ts, actionsDynamic, unstructuredObjs, targetGRs)
			},
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedConditions := []operatorv1.OperatorCondition{
					{
						Type:   "EncryptionMigrationControllerDegraded",
						Status: "False",
					},
					{
						Type:   "EncryptionMigrationControllerProgressing",
						Status: "False",
					},
					{
						Type:   "EncryptionStorageMigrationProgressing",
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
						Conditions: []operatorv1.OperatorCondition{
							operatorv1.OperatorCondition{
								Type:   "EncryptionMigrationControllerDegraded",
								Status: "False",
							},
							operatorv1.OperatorCondition{
								Type:   "EncryptionMigrationControllerProgressing",
								Status: operatorv1.ConditionFalse,
							},
						},
					},
				},
				nil,
				nil,
			)

			allResources := []runtime.Object{}
			allResources = append(allResources, scenario.initialResources...)
			for _, initialSecret := range scenario.initialSecrets {
				allResources = append(allResources, initialSecret)
			}
			fakeKubeClient := fake.NewSimpleClientset(allResources...)
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events(scenario.targetNamespace), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" and $targetNamespace ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed", scenario.targetNamespace)
			fakeSecretClient := fakeKubeClient.CoreV1()
			fakePodClient := fakeKubeClient.CoreV1().Pods(scenario.targetNamespace)

			// let dynamic client know about the resources we want to encrypt
			resourceRequiresEncyrptionFunc := func(kind string) bool {
				if len(kind) == 0 {
					return false
				}
				for gr, _ := range scenario.targetGRs {
					if strings.HasPrefix(gr.Resource, strings.ToLower(kind)) {
						return true
					}
				}
				return false
			}
			scheme := runtime.NewScheme()
			unstructuredObjs := []runtime.Object{}
			for _, rawObject := range allResources {
				rawUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rawObject.DeepCopyObject())
				if err != nil {
					t.Fatal(err)
				}
				unstructuredObj := &unstructured.Unstructured{Object: rawUnstructured}
				if resourceRequiresEncyrptionFunc(unstructuredObj.GetKind()) {
					unstructuredObjs = append(unstructuredObjs, unstructuredObj)
				}
			}
			fakeDynamicClient := dynamicfakeclient.NewSimpleDynamicClient(scheme, unstructuredObjs...)
			fakeDiscoveryClient := &fakeDisco{fakeKubeClient.Discovery(), []*metav1.APIResourceList{
				&metav1.APIResourceList{
					TypeMeta:     metav1.TypeMeta{},
					APIResources: scenario.targetAPIResources,
				},
			}}

			// act
			target := newEncryptionMigrationController(
				scenario.targetNamespace,
				fakeOperatorClient,
				kubeInformers,
				fakeSecretClient,
				scenario.encryptionSecretSelector,
				eventRecorder,
				scenario.targetGRs,
				fakePodClient,
				fakeDynamicClient,
				fakeDiscoveryClient,
			)
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

			if err := validateActionsVerbs(fakeKubeClient.Actions(), scenario.expectedActions); err != nil {
				t.Fatalf("incorrect action(s) detected: %v", err)
			}
			if scenario.validateFunc != nil {
				scenario.validateFunc(t, fakeKubeClient.Actions(), fakeDynamicClient.Actions(), scenario.initialSecrets, scenario.targetGRs, unstructuredObjs)
			}
			if scenario.validateOperatorClientFunc != nil {
				scenario.validateOperatorClientFunc(t, fakeOperatorClient)
			}
		})
	}
}

func validateMigratedResources(ts *testing.T, actions []clientgotesting.Action, unstructuredObjs []runtime.Object, targetGRs map[schema.GroupResource]bool) {
	ts.Helper()

	expectedActionsNoList := len(actions) - len(targetGRs) // subtract "list" requests
	if expectedActionsNoList != len(unstructuredObjs) {
		ts.Fatalf("incorrect number of resources were encrypted, expected %d, got %d", len(unstructuredObjs), expectedActionsNoList)
	}

	// validate LIST requests
	{
		validatedListRequests := 0
		for gr, _ := range targetGRs {
			for _, action := range actions {
				if action.Matches("list", gr.Resource) {
					validatedListRequests++
					break
				}
			}
		}
		if validatedListRequests != len(targetGRs) {
			ts.Fatalf("incorrect number of LIST request, expedted %d, got %d", len(targetGRs), validatedListRequests)
		}
	}

	// validate UPDATE requests
	for _, action := range actions {
		if action.GetVerb() == "update" {
			unstructuredObjValidated := false

			updateAction := action.(clientgotesting.UpdateAction)
			updatedObj := updateAction.GetObject().(*unstructured.Unstructured)
			for _, rawUnstructuredObj := range unstructuredObjs {
				expectedUnstructuredObj, ok := rawUnstructuredObj.(*unstructured.Unstructured)
				if !ok {
					ts.Fatalf("object %T is not *unstructured.Unstructured", expectedUnstructuredObj)
				}
				if equality.Semantic.DeepEqual(updatedObj, expectedUnstructuredObj) {
					unstructuredObjValidated = true
					break
				}
			}

			if !unstructuredObjValidated {
				ts.Fatalf("encrypted object with kind = %s, namespace = %s and name = %s wasn't expected to be encrypted", updatedObj.GetKind(), updatedObj.GetNamespace(), updatedObj.GetName())
			}
		}
	}
}

func validateSecretsWereAnnotated(ts *testing.T, actions []clientgotesting.Action, expectedSecrets []*corev1.Secret) {
	validatedSecrets := 0
	for _, action := range actions {
		if action.Matches("update", "secrets") {
			updateAction := action.(clientgotesting.UpdateAction)
			actualSecret := updateAction.GetObject().(*corev1.Secret)
			// this test assumes that the encryption key secret is annotated
			// thus for simplicity, we rewrite the annotation and compare the rest
			for _, expectedSecret := range expectedSecrets {
				if expectedSecret.Annotations == nil {
					expectedSecret.Annotations = map[string]string{}
				}
				expectedSecret.Annotations[encryptionSecretMigratedTimestampForTest] = actualSecret.Annotations[encryptionSecretMigratedTimestampForTest]

				if !equality.Semantic.DeepEqual(actualSecret, expectedSecret) {
					validatedSecrets++
					break
				}
			}
		}
	}
	if validatedSecrets != len(expectedSecrets) {
		ts.Errorf("%d secret(s) were validated but expected to validate %d", validatedSecrets, len(expectedSecrets))
	}
}

func createConfigMap(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

type fakeDisco struct {
	delegate           discovery.DiscoveryInterface
	serverPreferredRes []*metav1.APIResourceList
}

func (f *fakeDisco) RESTClient() interface{} {
	return f.delegate
}

func (f *fakeDisco) ServerGroups() (*metav1.APIGroupList, error) {
	return f.delegate.ServerGroups()
}

func (f *fakeDisco) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	return f.delegate.ServerResourcesForGroupVersion(groupVersion)
}

func (f *fakeDisco) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return f.delegate.ServerGroupsAndResources()
}

func (f *fakeDisco) ServerResources() ([]*metav1.APIResourceList, error) {
	return f.delegate.ServerResources()
}

func (f *fakeDisco) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return f.serverPreferredRes, nil
}

func (f *fakeDisco) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return f.delegate.ServerPreferredNamespacedResources()
}

func (f *fakeDisco) ServerVersion() (*version.Info, error) {
	return f.delegate.ServerVersion()
}

func (f *fakeDisco) OpenAPISchema() (*openapi_v2.Document, error) {
	return f.delegate.OpenAPISchema()
}
