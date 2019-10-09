package encryption

import (
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/utils/diff"
)

func TestGetDesiredEncryptionState(t *testing.T) {
	type args struct {
		oldEncryptionConfig *apiserverconfigv1.EncryptionConfiguration
		targetNamespace     string
		encryptionSecrets   []*corev1.Secret
		toBeEncryptedGRs    []schema.GroupResource
	}
	type ValidateState func(ts *testing.T, args *args, state groupResourcesState)

	nilState := func(ts *testing.T, _ *args, state groupResourcesState) {
		if state != nil {
			ts.Errorf("expected nil state, got: %#v", state)
		}
	}

	equalsConfig := func(expected *apiserverconfigv1.EncryptionConfiguration) func(ts *testing.T, args *args, state groupResourcesState) {
		return func(ts *testing.T, _ *args, state groupResourcesState) {
			if expected == nil && state != nil {
				ts.Errorf("expected nil state, got: %#v", state)
				return
			}
			if expected != nil && state == nil {
				ts.Errorf("expected non-nil state corresponding to config %#v", expected)
				return
			}
			if expected == nil && state == nil {
				return
			}
			expected := expected.DeepCopy()
			expected.TypeMeta = metav1.TypeMeta{}
			encryptionConfig := &apiserverconfigv1.EncryptionConfiguration{Resources: getResourceConfigs(state)}
			if !reflect.DeepEqual(expected, encryptionConfig) {
				ts.Errorf("state %#v does not match encryption config (A input, B output):\n%s", state, diff.ObjectDiff(expected, encryptionConfig))
			}
		}
	}

	outputMatchingInputConfig := func(ts *testing.T, args *args, state groupResourcesState) {
		equalsConfig(args.oldEncryptionConfig)(ts, args, state)
	}

	tests := []struct {
		name     string
		args     args
		validate ValidateState
	}{
		{
			"first run: no config, no secrets => nothing done, nil state returned",
			args{
				nil,
				"kms",
				nil,
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			nilState,
		},
		{
			"config exists, no secrets => nothing done, config unchanged",
			args{
				createEncryptionCfgNoWriteKey("1", "NzFlYTdjOTE0MTlhNjhmZDEyMjRmODhkNTAzMTZiNGU=", "configmaps", "secrets"),
				"kms",
				nil,
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			outputMatchingInputConfig,
		},
		{
			"config exists with only one resource => 2nd resource is added",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("71ea7c91419a68fd1224f88d50316b4e")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}},
					},
				},
			}),
		},
		{
			"config exists with two resources => 2nd resource stays",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}, {
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("71ea7c91419a68fd1224f88d50316b4e")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
		{
			"no config, secrets exist => first config is created",
			args{
				nil,
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("71ea7c91419a68fd1224f88d50316b4e")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("71ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}},
					},
				}}),
		},
		{
			"no config, multiple secrets exists, some migrated => config is recreated, with identity as write key",
			args{
				nil,
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 5, []byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}}, 4, []byte("447907494bßc4897b876c8476bf807bc"), time.Now()),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}}, 3, []byte("3cbfbe7d76876e076b076c659cd895ff"), time.Now()),
					createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}}, 2, []byte("2b234b23cb23c4b2cb24cb24bcbffbca")),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}}, 1, []byte("11ea7c91419a68fd1224f88d50316b4a"), time.Now()),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "4",
									Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "3",
									Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
								}},
							},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "4",
									Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "3",
									Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
								}},
							},
						}},
					},
				}}),
		},
		{
			"config exists, only some secret is missing => missing secret is not used as write key, but next most-recent key is",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{
						{
							Resources: []string{"configmaps"},
							Providers: []apiserverconfigv1.ProviderConfiguration{{
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "5",
										Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
									}},
								},
							}, {
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "4",
										Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
									}},
								},
							}, {
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "3",
										Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
									}},
								},
							}, {
								Identity: &apiserverconfigv1.IdentityConfiguration{},
							}},
						},
						{
							Resources: []string{"secrets"},
							Providers: []apiserverconfigv1.ProviderConfiguration{{
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "5",
										Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
									}},
								},
							}, {
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "4",
										Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
									}},
								},
							}, {
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "3",
										Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
									}},
								},
							}, {
								Identity: &apiserverconfigv1.IdentityConfiguration{},
							}},
						},
					}},
				"kms",
				[]*corev1.Secret{
					// missing: createEncryptionKeySecretWithRawKey("kms", nil, 5, []byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}}, 4, []byte("447907494bßc4897b876c8476bf807bc"), time.Now()),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}}, 3, []byte("3cbfbe7d76876e076b076c659cd895ff"), time.Now()),
					createEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}}, 2, []byte("2b234b23cb23c4b2cb24cb24bcbffbca")),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}}, 1, []byte("11ea7c91419a68fd1224f88d50316b4a"), time.Now()),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				// 4 is becoming new write key, not 5!
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "4",
									Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "3",
									Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "4",
									Secret: base64.StdEncoding.EncodeToString([]byte("447907494bßc4897b876c8476bf807bc")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "3",
									Secret: base64.StdEncoding.EncodeToString([]byte("3cbfbe7d76876e076b076c659cd895ff")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				}}),
		},
		{
			"config exists without identity => identity is appended",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{
						{
							Resources: []string{"configmaps"},
							Providers: []apiserverconfigv1.ProviderConfiguration{{
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "5",
										Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
									}},
								},
							}},
						},
						{
							Resources: []string{"secrets"},
							Providers: []apiserverconfigv1.ProviderConfiguration{{
								AESCBC: &apiserverconfigv1.AESConfiguration{
									Keys: []apiserverconfigv1.Key{{
										Name:   "5",
										Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
									}},
								},
							}},
						},
					}},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 5, []byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "5",
									Secret: base64.StdEncoding.EncodeToString([]byte("55b5bcbc85cb857c7c07c56c54983cbcd")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
		{
			"config exists, new key secret => new key added as read key",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}, {
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("11ea7c91419a68fd1224f88d50316b4e")),
					createEncryptionKeySecretWithRawKey("kms", nil, 2, []byte("2bc2bdbc2bec2ebce7b27ce792639723")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
		{
			"config exists, read keys are consistent => new write key is set",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}, {
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("11ea7c91419a68fd1224f88d50316b4e")),
					createEncryptionKeySecretWithRawKey("kms", nil, 2, []byte("2bc2bdbc2bec2ebce7b27ce792639723")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
		{
			"config exists, read+write keys are consistent, not migrated => nothing changes",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}, {
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("11ea7c91419a68fd1224f88d50316b4e")),
					createEncryptionKeySecretWithRawKey("kms", nil, 2, []byte("2bc2bdbc2bec2ebce7b27ce792639723")),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
		{
			"config exists, read+write keys are consistent, migrated => old read-keys are pruned from config",
			args{
				&apiserverconfigv1.EncryptionConfiguration{
					Resources: []apiserverconfigv1.ResourceConfiguration{{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}, {
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "1",
									Secret: base64.StdEncoding.EncodeToString([]byte("11ea7c91419a68fd1224f88d50316b4e")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					}},
				},
				"kms",
				[]*corev1.Secret{
					createEncryptionKeySecretWithRawKey("kms", nil, 1, []byte("11ea7c91419a68fd1224f88d50316b4e")),
					createMigratedEncryptionKeySecretWithRawKey("kms", []schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}}, 2, []byte("2bc2bdbc2bec2ebce7b27ce792639723"), time.Now()),
				},
				[]schema.GroupResource{{Group: "", Resource: "configmaps"}, {Group: "", Resource: "secrets"}},
			},
			equalsConfig(&apiserverconfigv1.EncryptionConfiguration{
				Resources: []apiserverconfigv1.ResourceConfiguration{
					{
						Resources: []string{"configmaps"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
					{
						Resources: []string{"secrets"},
						Providers: []apiserverconfigv1.ProviderConfiguration{{
							AESCBC: &apiserverconfigv1.AESConfiguration{
								Keys: []apiserverconfigv1.Key{{
									Name:   "2",
									Secret: base64.StdEncoding.EncodeToString([]byte("2bc2bdbc2bec2ebce7b27ce792639723")),
								}},
							},
						}, {
							Identity: &apiserverconfigv1.IdentityConfiguration{},
						}},
					},
				},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDesiredEncryptionState(tt.args.oldEncryptionConfig, tt.args.targetNamespace, tt.args.encryptionSecrets, tt.args.toBeEncryptedGRs)
			if tt.validate != nil {
				tt.validate(t, &tt.args, got)
			}
		})
	}
}
