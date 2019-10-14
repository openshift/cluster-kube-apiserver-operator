package encryption

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

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

func TestStateController(t *testing.T) {
	scenarios := []struct {
		name                     string
		initialResources         []runtime.Object
		encryptionSecretSelector metav1.ListOptions
		targetNamespace          string
		targetGRs                []schema.GroupResource
		// expectedActions holds actions to be verified in the form of "verb:resource:namespace"
		expectedActions []string
		// destName denotes the name of the secret that contains EncryptionConfiguration
		// this field is required to create the controller
		destName                   string
		expectedEncryptionCfg      *apiserverconfigv1.EncryptionConfiguration
		validateFunc               func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration)
		validateOperatorClientFunc func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient)
		expectedError              error
	}{
		// scenario 1: validates if "encryption-config-kube-apiserver-test" secret with EncryptionConfiguration in "openshift-config-managed" namespace
		// was not created when no secrets with encryption keys are present in that namespace.
		{
			name:            "no secret with EncryptionConfig is created when there are no secrets with the encryption keys",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed"},
		},

		// scenario 2: validates if "encryption-config-kube-apiserver-test" secret with EncryptionConfiguration in "openshift-config-managed" namespace is created,
		// it also checks the content and the order of encryption providers, this test expects identity first and aescbc second
		{
			name:                     "secret with EncryptionConfig is created without a write key",
			targetNamespace:          "kms",
			encryptionSecretSelector: metav1.ListOptions{LabelSelector: "encryption.apiserver.operator.openshift.io/component=kms"},
			destName:                 "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 1, []byte("61def964fb967f5d7c44a2af8dab6865")),
			},
			expectedActions:       []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "create:secrets:openshift-config-managed", "create:events:kms"},
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

		// scenario 3
		{
			name:            "secret with EncryptionConfig is created and it contains a single write key",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("171582a0fcd6c5fdb65cbf5a3e9249d7")),
				func() *corev1.Secret {
					ec := createEncryptionCfgNoWriteKey("34", "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=", "secrets")
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
			},
			expectedEncryptionCfg: func() *apiserverconfigv1.EncryptionConfiguration {
				keysRes := keysResourceModes{
					resource: "secrets",
					keys: []apiserverconfigv1.Key{
						{
							Name:   "34",
							Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
						},
					},
				}
				ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
				return ec
			}(),
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "create:secrets:openshift-config-managed", "create:events:kms"},
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

		// scenario 4
		{
			name:            "no-op when no key is transitioning",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("171582a0fcd6c5fdb65cbf5a3e9249d7"), time.Now()),
				func() *corev1.Secret {
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "34",
								Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
				func() *corev1.Secret {
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "34",
								Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed"},
		},

		// scenario 5
		{
			name:            "the key with ID=34 is transitioning (observed as a read key) so it is used as a write key in the EncryptionConfig",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 33, []byte("171582a0fcd6c5fdb65cbf5a3e9249d7")),
				createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("dda090c18770163d57d6aaca85f7b3a5")),
				func() *corev1.Secret { // encryption config in kms namespace
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "33",
								Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
							},
							{
								Name:   "34",
								Secret: "ZGRhMDkwYzE4NzcwMTYzZDU3ZDZhYWNhODVmN2IzYTU=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
				func() *corev1.Secret { // encryption config in openshift-config-managed
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "33",
								Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
							},
							{
								Name:   "34",
								Secret: "ZGRhMDkwYzE4NzcwMTYzZDU3ZDZhYWNhODVmN2IzYTU=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedEncryptionCfg: func() *apiserverconfigv1.EncryptionConfiguration {
				keysRes := keysResourceModes{
					resource: "secrets",
					keys: []apiserverconfigv1.Key{
						{
							Name:   "34",
							Secret: "ZGRhMDkwYzE4NzcwMTYzZDU3ZDZhYWNhODVmN2IzYTU=",
						},
						{
							Name:   "33",
							Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
						},
					},
				}
				ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
				return ec
			}(),
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("update", "secrets") {
						updateAction := action.(clientgotesting.UpdateAction)
						actualSecret := updateAction.GetObject().(*corev1.Secret)
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

		// scenario 6
		{
			name:            "checks if the order of the keys is preserved and that they read keys are pruned - all migrated",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 31, []byte("a1f1b3e36c477d91ea85af0f32358f70")),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 32, []byte("42b07b385a0edee268f1ac41cfc53857")),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 33, []byte("b0af82240e10c032fd9bbbedd3b5955a")),
				createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("1c06e8517890c8dc44f627905efc86b8"), time.Now()),
				func() *corev1.Secret { // encryption config in kms namespace
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "34",
								Secret: "MWMwNmU4NTE3ODkwYzhkYzQ0ZjYyNzkwNWVmYzg2Yjg=",
							},
							{
								Name:   "33",
								Secret: "YjBhZjgyMjQwZTEwYzAzMmZkOWJiYmVkZDNiNTk1NWE=",
							},
							{
								Name:   "32",
								Secret: "NDJiMDdiMzg1YTBlZGVlMjY4ZjFhYzQxY2ZjNTM4NTc=",
							},
							{
								Name:   "31",
								Secret: "YTFmMWIzZTM2YzQ3N2Q5MWVhODVhZjBmMzIzNThmNzA=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
				func() *corev1.Secret { // encryption config in openshift-config-managed namespace
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "34",
								Secret: "MWMwNmU4NTE3ODkwYzhkYzQ0ZjYyNzkwNWVmYzg2Yjg=",
							},
							{
								Name:   "33",
								Secret: "YjBhZjgyMjQwZTEwYzAzMmZkOWJiYmVkZDNiNTk1NWE=",
							},
							{
								Name:   "32",
								Secret: "NDJiMDdiMzg1YTBlZGVlMjY4ZjFhYzQxY2ZjNTM4NTc=",
							},
							{
								Name:   "31",
								Secret: "YTFmMWIzZTM2YzQ3N2Q5MWVhODVhZjBmMzIzNThmNzA=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedEncryptionCfg: func() *apiserverconfigv1.EncryptionConfiguration {
				keysRes := keysResourceModes{
					resource: "secrets",
					keys: []apiserverconfigv1.Key{
						{
							Name:   "34",
							Secret: "MWMwNmU4NTE3ODkwYzhkYzQ0ZjYyNzkwNWVmYzg2Yjg=",
						},
					},
				}
				ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
				return ec
			}(),
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("update", "secrets") {
						updateAction := action.(clientgotesting.UpdateAction)
						actualSecret := updateAction.GetObject().(*corev1.Secret)
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

		// scenario 7
		{
			name:            "checks if the order of the keys is preserved - with a key that is transitioning",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 31, []byte("a1f1b3e36c477d91ea85af0f32358f70")),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 32, []byte("42b07b385a0edee268f1ac41cfc53857")),
				createExpiredMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 33, []byte("b0af82240e10c032fd9bbbedd3b5955a")),
				createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("1c06e8517890c8dc44f627905efc86b8")),
				func() *corev1.Secret { // encryption config in kms namespace
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "33",
								Secret: base64.StdEncoding.EncodeToString([]byte("b0af82240e10c032fd9bbbedd3b5955a")),
							},
							{
								Name:   "34",
								Secret: base64.StdEncoding.EncodeToString([]byte("1c06e8517890c8dc44f627905efc86b8")),
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
				func() *corev1.Secret { // encryption config in openshift-config-managed namespace
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "33",
								Secret: base64.StdEncoding.EncodeToString([]byte("b0af82240e10c032fd9bbbedd3b5955a")),
							},
							{
								Name:   "34",
								Secret: base64.StdEncoding.EncodeToString([]byte("1c06e8517890c8dc44f627905efc86b8")),
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedEncryptionCfg: func() *apiserverconfigv1.EncryptionConfiguration {
				keysRes := keysResourceModes{
					resource: "secrets",
					keys: []apiserverconfigv1.Key{
						{
							Name:   "34",
							Secret: base64.StdEncoding.EncodeToString([]byte("1c06e8517890c8dc44f627905efc86b8")),
						},
						{
							Name:   "33",
							Secret: base64.StdEncoding.EncodeToString([]byte("b0af82240e10c032fd9bbbedd3b5955a")),
						},
					},
				}
				ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
				return ec
			}(),
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration) {
				wasSecretValidated := false
				for _, action := range actions {
					if action.Matches("update", "secrets") {
						updateAction := action.(clientgotesting.UpdateAction)
						actualSecret := updateAction.GetObject().(*corev1.Secret)
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

		// scenario 8
		//
		// BUG: this test simulates deletion of an encryption config in the target ns - the encryption config had a single secret
		//      as a result a new encryption config is created with a single read key - that effectively means that the encryption was turned off (temporarily)
		{
			name:            "no encryption cfg in the target ns (was deleted)",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 34, []byte("171582a0fcd6c5fdb65cbf5a3e9249d7"), time.Now()),
				func() *corev1.Secret {
					keysRes := keysResourceModes{
						resource: "secrets",
						keys: []apiserverconfigv1.Key{
							{
								Name:   "34",
								Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedEncryptionCfg: func() *apiserverconfigv1.EncryptionConfiguration {
				keysRes := keysResourceModes{
					resource: "secrets",
					keys: []apiserverconfigv1.Key{
						{
							Name:   "34",
							Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
						},
					},
				}
				ec := createEncryptionCfgWithWriteKey([]keysResourceModes{keysRes})
				return ec
			}(),
			validateFunc: func(ts *testing.T, actions []clientgotesting.Action, destName string, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration) {
				// TODO: fix the temporary identity key on config reconstruction in getDesiredEncryptionState
				/*
					wasSecretValidated := false
					for _, action := range actions {
						if action.Matches("update", "secrets") {
							updateAction := action.(clientgotesting.UpdateAction)
							actualSecret := updateAction.GetObject().(*corev1.Secret)
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
				*/
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed", "update:secrets:openshift-config-managed", "create:events:kms"},
		},

		// scenario 9
		//
		// verifies if removing a target GR doesn't have effect - we will keep encrypting that GR
		{
			name:            "a user can't stop encrypting config maps",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPod("kube-apiserver-1", "kms"),
				createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}, {Group: "", Resource: "configmaps"}}, 34, []byte("171582a0fcd6c5fdb65cbf5a3e9249d7"), time.Now()),
				func() *corev1.Secret { // encryption config in kms namespace
					keysRes := []keysResourceModes{
						{
							resource: "configmaps",
							keys: []apiserverconfigv1.Key{
								{
									Name:   "34",
									Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
								},
							},
						},
						{
							resource: "secrets",
							keys: []apiserverconfigv1.Key{
								{
									Name:   "34",
									Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
								},
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey(keysRes)
					ecs := createEncryptionCfgSecret(t, "kms", "1", ec)
					return ecs
				}(),
				func() *corev1.Secret { // encryption config in openshift-config-managed namespace
					keysRes := []keysResourceModes{
						{
							resource: "configmaps",
							keys: []apiserverconfigv1.Key{
								{
									Name:   "34",
									Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
								},
							},
						},
						{
							resource: "secrets",
							keys: []apiserverconfigv1.Key{
								{
									Name:   "34",
									Secret: "MTcxNTgyYTBmY2Q2YzVmZGI2NWNiZjVhM2U5MjQ5ZDc=",
								},
							},
						},
					}
					ec := createEncryptionCfgWithWriteKey(keysRes)
					ecs := createEncryptionCfgSecret(t, "openshift-config-managed", "1", ec)
					ecs.Name = "encryption-config-kube-apiserver-test"
					return ecs
				}(),
			},
			expectedActions: []string{"list:pods:kms", "get:secrets:kms", "list:secrets:openshift-config-managed", "get:secrets:openshift-config-managed"},
		},

		// scenario 10
		{
			name:            "degraded a pod with invalid condition",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createDummyKubeAPIPodInUnknownPhase("kube-apiserver-1", "kms"),
			},
			expectedActions: []string{"list:pods:kms"},
			expectedError:   errors.New("api server pod kube-apiserver-1 in unknown phase"),
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedCondition := operatorv1.OperatorCondition{
					Type:    "EncryptionStateControllerDegraded",
					Status:  "True",
					Reason:  "Error",
					Message: "api server pod kube-apiserver-1 in unknown phase",
				}
				validateOperatorClientConditions(ts, operatorClient, []operatorv1.OperatorCondition{expectedCondition})
			},
		},

		// scenario 11
		{
			name:            "no-op as an invalid secret is not considered",
			targetNamespace: "kms",
			destName:        "encryption-config-kube-apiserver-test",
			targetGRs: []schema.GroupResource{
				{Group: "", Resource: "secrets"},
			},
			initialResources: []runtime.Object{
				createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "secrets"}}, 1, []byte("")),
			},
			expectedActions: []string{"list:pods:kms"},
			validateOperatorClientFunc: func(ts *testing.T, operatorClient v1helpers.StaticPodOperatorClient) {
				expectedCondition := operatorv1.OperatorCondition{
					Type:   "EncryptionStateControllerDegraded",
					Status: "False",
				}
				validateOperatorClientConditions(ts, operatorClient, []operatorv1.OperatorCondition{expectedCondition})
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
							{
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
			eventRecorder := events.NewRecorder(fakeKubeClient.CoreV1().Events(scenario.targetNamespace), "test-encryptionKeyController", &corev1.ObjectReference{})
			// we pass "openshift-config-managed" and $targetNamespace ns because the controller creates an informer for secrets in that namespace.
			// note that the informer factory is not used in the test - it's only needed to create the controller
			kubeInformers := v1helpers.NewKubeInformersForNamespaces(fakeKubeClient, "openshift-config-managed", scenario.targetNamespace)
			fakeSecretClient := fakeKubeClient.CoreV1()
			fakePodClient := fakeKubeClient.CoreV1()

			target := newStateController(
				scenario.targetNamespace, scenario.destName,
				fakeOperatorClient,
				kubeInformers,
				fakeSecretClient,
				fakePodClient,
				scenario.encryptionSecretSelector,
				eventRecorder,
				scenario.targetGRs,
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
				scenario.validateFunc(t, fakeKubeClient.Actions(), scenario.destName, scenario.expectedEncryptionCfg)
			}
			if scenario.validateOperatorClientFunc != nil {
				scenario.validateOperatorClientFunc(t, fakeOperatorClient)
			}
		})
	}
}

func validateSecretWithEncryptionConfig(actualSecret *corev1.Secret, expectedEncryptionCfg *apiserverconfigv1.EncryptionConfiguration, expectedSecretName string) error {
	actualEncryptionCfg, err := secretDataToEncryptionConfig(actualSecret)
	if err != nil {
		return fmt.Errorf("failed to verfy the encryption config, due to %v", err)
	}

	if !equality.Semantic.DeepEqual(expectedEncryptionCfg, actualEncryptionCfg) {
		return fmt.Errorf("%s", diff.ObjectDiff(expectedEncryptionCfg, actualEncryptionCfg))
	}

	// rewrite the payload and compare the rest
	expectedSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      expectedSecretName,
			Namespace: "openshift-config-managed",
			Annotations: map[string]string{
				kubernetesDescriptionKey: kubernetesDescriptionScaryValue,
			},
			Finalizers: []string{"encryption.apiserver.operator.openshift.io/deletion-protection"},
		},
		Data: actualSecret.Data,
	}

	// those are filled by the server
	if len(actualSecret.Kind) == 0 {
		actualSecret.Kind = "Secret"
	}
	if len(actualSecret.APIVersion) == 0 {
		actualSecret.APIVersion = corev1.SchemeGroupVersion.String()
	}

	if !equality.Semantic.DeepEqual(expectedSecret, actualSecret) {
		return fmt.Errorf("%s", diff.ObjectDiff(expectedSecret, actualSecret))
	}

	return nil
}
