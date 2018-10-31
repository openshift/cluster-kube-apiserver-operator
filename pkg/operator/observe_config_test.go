package operator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	v1alpha12 "github.com/openshift/api/operator/v1alpha1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	clusterkubeapiserverfake "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

func TestObserveClusterConfig(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	indexer.Add(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: podCIDR \n  serviceCIDR: serviceCIDR\n",
		},
	})
	listers := Listers{
		configmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errors := observeRestrictedCIDRs(listers, map[string]interface{}{})
	if len(errors) > 0 {
		t.Error("expected len(errors) == 0")
	}
	restrictedCIDRs, _, err := unstructured.NestedSlice(result, "admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedCIDRs[0] != "podCIDR" {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != "serviceCIDR" {
		t.Error(restrictedCIDRs[1])
	}
}

func TestSyncStatus(t *testing.T) {

	testCases := []struct {
		name                   string
		clusterConfigV1        *corev1.ConfigMap
		etcd                   *corev1.Endpoints
		operatorConfig         *v1alpha1.KubeAPIServerOperatorConfig
		imageConfig            *configv1.Image
		expectError            bool
		expectedObservedConfig *unstructured.Unstructured
		expectedCondition      *v1alpha12.OperatorCondition
	}{
		{
			name:            "HappyPath",
			clusterConfigV1: newClusterConfigV1ConfigMap(),
			etcd:            newEtcdEndpoints(),
			operatorConfig:  newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:     newImageConfig(),
			expectError:     false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_POD_CIDR",
								"OBSERVED_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"https://OBSERVED_ETCD_HOSTNAME.OBSERVED_DNS_SUFFIX:2379",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:   v1alpha12.OperatorStatusTypeFailing,
				Status: v1alpha12.ConditionFalse,
			},
		},
		{
			name:            "MissingEndpoints",
			clusterConfigV1: newClusterConfigV1ConfigMap(),
			operatorConfig:  newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:     newImageConfig(),
			expectError:     false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_POD_CIDR",
								"OBSERVED_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"ORIGINAL_STORAGE_URL",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "endpoints/etcd.kube-system: not found",
			},
		},
		{
			name:            "MissingDNSSuffix",
			clusterConfigV1: newClusterConfigV1ConfigMap(),
			etcd: &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "etcd"},
				Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{Hostname: "OBSERVED_ETCD_HOSTNAME"}}}},
			},
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_POD_CIDR",
								"OBSERVED_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"ORIGINAL_STORAGE_URL",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "endpoints/etcd.kube-system: alpha.installer.openshift.io/dns-suffix annotation not found",
			},
		},
		{
			name:            "MissingEndpointHostname",
			clusterConfigV1: newClusterConfigV1ConfigMap(),
			etcd: &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "etcd", Annotations: map[string]string{"alpha.installer.openshift.io/dns-suffix": "OBSERVED_DNS_SUFFIX"}},
				Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{Hostname: "OBSERVED_ETCD_HOSTNAME"}, {IP: "OBSERVED_ETCD_IP"}}}},
			},
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_POD_CIDR",
								"OBSERVED_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"ORIGINAL_STORAGE_URL",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "endpoints/etcd.kube-system: subsets[0]addresses[1].hostname not found",
			},
		},
		{
			name:           "MissingClusterConfigV1",
			etcd:           newEtcdEndpoints(),
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"https://OBSERVED_ETCD_HOSTNAME.OBSERVED_DNS_SUFFIX:2379",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "configmap/cluster-config-v1.kube-system: not found",
			},
		},
		{
			name: "MissingPodCIDR",
			clusterConfigV1: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
				Data:       map[string]string{"install-config": "networking:\n  serviceCIDR: OBSERVED_SERVICE_CIDR\n"},
			},
			etcd:           newEtcdEndpoints(),
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"https://OBSERVED_ETCD_HOSTNAME.OBSERVED_DNS_SUFFIX:2379",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "configmap/cluster-config-v1.kube-system: install-config/networking/podCIDR not found",
			},
		},
		{
			name: "MissingServiceCIDR",
			clusterConfigV1: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
				Data:       map[string]string{"install-config": "networking:\n  podCIDR: OBSERVED_POD_CIDR\n"},
			},
			etcd:           newEtcdEndpoints(),
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"OBSERVED_POD_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"https://OBSERVED_ETCD_HOSTNAME.OBSERVED_DNS_SUFFIX:2379",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "configmap/cluster-config-v1.kube-system: install-config/networking/serviceCIDR not found",
			},
		},
		{
			name: "MissingAllCIDRs",
			clusterConfigV1: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
				Data:       map[string]string{"install-config": "networking: []\n"},
			},
			etcd:           newEtcdEndpoints(),
			operatorConfig: newInstanceKubeAPIServerOperatorConfig(),
			imageConfig:    newImageConfig(),
			expectError:    false,
			expectedObservedConfig: &unstructured.Unstructured{Object: map[string]interface{}{
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"https://OBSERVED_ETCD_HOSTNAME.OBSERVED_DNS_SUFFIX:2379",
					},
				},
			}},
			expectedCondition: &v1alpha12.OperatorCondition{
				Type:    v1alpha12.OperatorStatusTypeFailing,
				Status:  v1alpha12.ConditionTrue,
				Reason:  configObservationErrorConditionReason,
				Message: "configmap/cluster-config-v1.kube-system: install-config/networking/podCIDR not found\nconfigmap/cluster-config-v1.kube-system: install-config/networking/serviceCIDR not found",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			endpointsIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tc.etcd != nil {
				endpointsIndexer.Add(tc.etcd)
			}
			configMapIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tc.clusterConfigV1 != nil {
				configMapIndexer.Add(tc.clusterConfigV1)
			}
			imagesIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tc.imageConfig != nil {
				imagesIndexer.Add(tc.imageConfig)
			}

			operatorConfigClient := clusterkubeapiserverfake.NewSimpleClientset(tc.operatorConfig)

			configObserver := ConfigObserver{
				listers: Listers{
					imageConfigLister: configlistersv1.NewImageLister(imagesIndexer),
					endpointsLister:   corelistersv1.NewEndpointsLister(endpointsIndexer),
					configmapLister:   corelistersv1.NewConfigMapLister(configMapIndexer),
				},
				operatorConfigClient: operatorConfigClient.KubeapiserverV1alpha1(),
				observers: []observeConfigFunc{
					observeStorageURLs,
					observeRestrictedCIDRs,
					observeInternalRegistryHostname,
				},
			}
			err := configObserver.sync()
			if tc.expectError && err == nil {
				t.Fatal("error expected")
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := operatorConfigClient.KubeapiserverV1alpha1().KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err.Error())
			}
			if !reflect.DeepEqual(tc.expectedObservedConfig, result.Spec.ObservedConfig.Object) {
				t.Errorf("\n===== observed config expected:\n%v\n===== observed config actual:\n%v", toYAML(tc.expectedObservedConfig), toYAML(result.Spec.ObservedConfig.Object))
			}
			condition := v1alpha1helpers.FindOperatorCondition(result.Status.Conditions, v1alpha12.OperatorStatusTypeFailing)
			if !reflect.DeepEqual(tc.expectedCondition, condition) {
				t.Fatalf("\n===== condition expected:\n%v\n===== condition actual:\n%v", toYAML(tc.expectedCondition), toYAML(condition))
			}
		})
	}
}

func TestSyncUpdateFailed(t *testing.T) {

	endpointsIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	endpointsIndexer.Add(newEtcdEndpoints())
	configMapIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	configMapIndexer.Add(newClusterConfigV1ConfigMap())
	kubeAPIServerOperatorConfig := newInstanceKubeAPIServerOperatorConfig()
	operatorConfigClient := clusterkubeapiserverfake.NewSimpleClientset(kubeAPIServerOperatorConfig)
	errOnUpdate := true
	operatorConfigClient.PrependReactor("update", "kubeapiserveroperatorconfigs", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
		if errOnUpdate {
			errOnUpdate = false
			return true, kubeAPIServerOperatorConfig, fmt.Errorf("TEST ERROR")
		}
		return false, nil, nil
	})
	configObserver := ConfigObserver{
		listers: Listers{
			endpointsLister: corelistersv1.NewEndpointsLister(endpointsIndexer),
			configmapLister: corelistersv1.NewConfigMapLister(configMapIndexer),
		},
		operatorConfigClient: operatorConfigClient.KubeapiserverV1alpha1(),
		observers: []observeConfigFunc{
			observeStorageURLs,
			observeRestrictedCIDRs,
		},
	}
	expectedObservedConfig := &unstructured.Unstructured{Object: map[string]interface{}{
		"admissionPluginConfig": map[string]interface{}{
			"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
				"configuration": map[string]interface{}{
					"restrictedCIDRs": []interface{}{
						"ORIGINAL_POD_CIDR",
						"ORIGINAL_SERVICE_CIDR",
					},
				},
			},
		},
		"imagePolicyConfig": map[string]interface{}{
			"internalRegistryHostname": "ORIGINAL_INTERNAL_REGISTRY_HOSTNAME",
		},
		"storageConfig": map[string]interface{}{
			"urls": []interface{}{
				"ORIGINAL_STORAGE_URL",
			},
		},
	}}
	expectedCondition := &v1alpha12.OperatorCondition{
		Type:    v1alpha12.OperatorStatusTypeFailing,
		Status:  v1alpha12.ConditionTrue,
		Reason:  configObservationErrorConditionReason,
		Message: "kubeapiserveroperatorconfigs/instance: error writing updated observed config: TEST ERROR",
	}
	err := configObserver.sync()
	if err != nil {
		t.Fatalf("error not expected: %v", err)
	}
	result, err := operatorConfigClient.KubeapiserverV1alpha1().KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err.Error())
	}
	observedConfig := map[string]interface{}{}
	json.NewDecoder(bytes.NewBuffer(result.Spec.ObservedConfig.Raw)).Decode(&observedConfig)
	if !reflect.DeepEqual(expectedObservedConfig.Object, observedConfig) {
		t.Errorf("\n===== observed config expected:\n%v\n===== observed config actual:\n%v", toYAML(expectedObservedConfig.Object), toYAML(observedConfig))
	}
	condition := v1alpha1helpers.FindOperatorCondition(result.Status.Conditions, v1alpha12.OperatorStatusTypeFailing)
	if !reflect.DeepEqual(expectedCondition, condition) {
		t.Fatalf("\n===== condition expected:\n%v\n===== condition actual:\n%v", toYAML(expectedCondition), toYAML(condition))
	}
}

func jsonMarshallOrPanic(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func newInstanceKubeAPIServerOperatorConfig() *v1alpha1.KubeAPIServerOperatorConfig {
	return &v1alpha1.KubeAPIServerOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "instance",
		},
		Spec: v1alpha1.KubeAPIServerOperatorConfigSpec{
			ObservedConfig: runtime.RawExtension{Raw: jsonMarshallOrPanic(map[string]interface{}{
				"admissionPluginConfig": map[string]interface{}{
					"openshift.io/RestrictedEndpointsAdmission": map[string]interface{}{
						"configuration": map[string]interface{}{
							"restrictedCIDRs": []interface{}{
								"ORIGINAL_POD_CIDR",
								"ORIGINAL_SERVICE_CIDR",
							},
						},
					},
				},
				"imagePolicyConfig": map[string]interface{}{
					"internalRegistryHostname": "ORIGINAL_INTERNAL_REGISTRY_HOSTNAME",
				},
				"storageConfig": map[string]interface{}{
					"urls": []interface{}{
						"ORIGINAL_STORAGE_URL",
					},
				},
			})},
		},
		Status: v1alpha1.KubeAPIServerOperatorConfigStatus{
			StaticPodOperatorStatus: v1alpha12.StaticPodOperatorStatus{
				OperatorStatus: v1alpha12.OperatorStatus{
					Conditions: []v1alpha12.OperatorCondition{
						{
							Type:    v1alpha12.OperatorStatusTypeFailing,
							Status:  v1alpha12.ConditionTrue,
							Reason:  configObservationErrorConditionReason,
							Message: "Condition set by test",
						},
					},
				},
			},
		},
	}
}

func newClusterConfigV1ConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "cluster-config-v1",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: OBSERVED_POD_CIDR\n  serviceCIDR: OBSERVED_SERVICE_CIDR\n",
		},
	}
}

func newEtcdEndpoints() *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "kube-system",
			Name:        "etcd",
			Annotations: map[string]string{"alpha.installer.openshift.io/dns-suffix": "OBSERVED_DNS_SUFFIX"},
		},
		Subsets: []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{{Hostname: "OBSERVED_ETCD_HOSTNAME"}}},
		},
	}
}

func newImageConfig() *configv1.Image {
	return &configv1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: configv1.ImageStatus{
			InternalRegistryHostname: "OBSERVED_INTERNAL_REGISTRY_HOSTNAME",
		},
	}
}

func toYAML(o interface{}) string {
	b, e := yaml.Marshal(o)
	if e != nil {
		return e.Error()
	}
	return string(b)
}

func TestObserveRegistryConfig(t *testing.T) {
	const (
		expectedInternalRegistryHostname = "docker-registry.openshift-image-registry.svc.cluster.local:5000"
	)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	imageConfig := &configv1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: configv1.ImageStatus{
			InternalRegistryHostname: expectedInternalRegistryHostname,
		},
	}
	indexer.Add(imageConfig)

	listers := Listers{
		imageConfigLister: configlistersv1.NewImageLister(indexer),
	}
	result, errs := observeInternalRegistryHostname(listers, map[string]interface{}{})
	if len(errs) > 0 {
		t.Error("expected len(errs) == 0")
	}
	internalRegistryHostname, _, err := unstructured.NestedString(result, "imagePolicyConfig", "internalRegistryHostname")
	if err != nil {
		t.Fatal(err)
	}
	if internalRegistryHostname != expectedInternalRegistryHostname {
		t.Errorf("expected internal registry hostname: %s, got %s", expectedInternalRegistryHostname, internalRegistryHostname)
	}
}
