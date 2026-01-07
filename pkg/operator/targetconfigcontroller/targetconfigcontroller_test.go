package targetconfigcontroller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/utils/clock"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/api/annotations"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/stretchr/testify/require"
	clientgotesting "k8s.io/client-go/testing"
)

var codec = scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)

func TestIsRequiredConfigPresent(t *testing.T) {
	tests := []struct {
		name          string
		config        string
		expectedError string
	}{
		{
			name: "unparseable",
			config: `{
		 "servingInfo": {
		}
		`,
			expectedError: "error parsing config",
		},
		{
			name:          "empty",
			config:        ``,
			expectedError: "no observedConfig",
		},
		{
			name: "nil-storage-urls",
			config: `{
		 "servingInfo": {
		   "namedCertificates": [
		     {
		       "certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
		       "keyFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"
		     }
		   ]
		 },
		 "admission": {"pluginConfig": { "network.openshift.io/RestrictedEndpointsAdmission": {}}},
		 "apiServerArguments": {
		   "etcd-servers": null
		 }
		}
		`,
			expectedError: "apiServerArguments.etcd-servers null in config",
		},
		{
			name: "missing-storage-urls",
			config: `{
		 "servingInfo": {
		   "namedCertificates": [
		     {
		       "certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
		       "keyFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"
		     }
		   ]
		 },
        "admission": {"pluginConfig": { "network.openshift.io/RestrictedEndpointsAdmission": {}}},
		 "apiServerArguments": {
		   "etcd-servers": []
		 }
		}
		`,
			expectedError: "apiServerArguments.etcd-servers empty in config",
		},
		{
			name: "empty-string-storage-urls",
			config: `{
  "servingInfo": {
    "namedCertificates": [
      {
        "certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
        "keyFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"
      }
    ]
  },
  "admission": {"pluginConfig": { "network.openshift.io/RestrictedEndpointsAdmission": {}}},
  "apiServerArguments": {
    "etcd-servers": ""
  }
}
`,
			expectedError: "apiServerArguments.etcd-servers empty in config",
		},
		{
			name: "good",
			config: `{
		 "servingInfo": {
		   "namedCertificates": [
		     {
		       "certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
		       "keyFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"
		     }
		   ]
		 },
         "admission": {"pluginConfig": { "network.openshift.io/RestrictedEndpointsAdmission": {}}},
		 "apiServerArguments": {
		   "etcd-servers": [ "val" ]
		 }
		}
		`,
		},
	}

	c := TargetConfigController{}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := c.isRequiredConfigPresent([]byte(test.config), false)
			switch {
			case actual == nil && len(test.expectedError) == 0:
			case actual == nil && len(test.expectedError) != 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) == 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) != 0 && !strings.Contains(actual.Error(), test.expectedError):
				t.Fatal(actual)
			}
		})
	}
}

var configWithWatchTerminationDuration = `
{
  "gracefulTerminationDuration": "135"
}
`

var configWithOverriddenWatchTerminationDuration = `
{
  "gracefulTerminationDuration": "275"
}
`

func TestManageTemplate(t *testing.T) {
	scenarios := []struct {
		name         string
		template     string
		golden       string
		operatorSpec *operatorv1.StaticPodOperatorSpec
	}{

		// scenario 1
		{
			name:         "happy path: default values are applied",
			template:     "{{.Image}}, {{.OperatorImage}}, {{.Verbosity}}, {{.GracefulTerminationDuration}}",
			golden:       "CaptainAmerica, Piper,  -v=2, 135",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{}},
		},

		// scenario 2
		{
			name:     "values from the observed configs are applied",
			template: "{{.Image}}, {{.OperatorImage}}, {{.Verbosity}}, {{.GracefulTerminationDuration}}",
			golden:   "CaptainAmerica, Piper,  -v=2, 135",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{
				ObservedConfig: runtime.RawExtension{Raw: []byte(configWithWatchTerminationDuration)},
			}},
		},

		// scenario 3
		{
			name:     "the GracefulTerminationDuration is extended due to a known AWS issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804a",
			template: "{{.Image}}, {{.OperatorImage}}, {{.Verbosity}}, {{.GracefulTerminationDuration}}",
			golden:   "CaptainAmerica, Piper,  -v=2, 275",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{
				ObservedConfig:             runtime.RawExtension{Raw: []byte(configWithWatchTerminationDuration)},
				UnsupportedConfigOverrides: runtime.RawExtension{Raw: []byte(configWithOverriddenWatchTerminationDuration)},
			}},
		},
		{
			name:     "default value provided for gogc",
			template: "{{.GOGC}}",
			golden:   "100",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ObservedConfig: runtime.RawExtension{Raw: []byte(`{}`)},
				},
			},
		},
		{
			name:     "gogc from unsupportedConfigOverrides",
			template: "{{.GOGC}}",
			golden:   "76",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ObservedConfig:             runtime.RawExtension{Raw: []byte(`{}`)},
					UnsupportedConfigOverrides: runtime.RawExtension{Raw: []byte(`{"garbageCollectionTargetPercentage":"76"}`)},
				},
			},
		},
		{
			name:     "gogc from unsupportedConfigOverrides clamped to lower bound",
			template: "{{.GOGC}}",
			golden:   "63",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ObservedConfig:             runtime.RawExtension{Raw: []byte(`{}`)},
					UnsupportedConfigOverrides: runtime.RawExtension{Raw: []byte(`{"garbageCollectionTargetPercentage":"62"}`)},
				},
			},
		},
		{
			name:     "gogc from unsupportedConfigOverrides clamped to upper bound",
			template: "{{.GOGC}}",
			golden:   "100",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{
				OperatorSpec: operatorv1.OperatorSpec{
					ObservedConfig:             runtime.RawExtension{Raw: []byte(`{}`)},
					UnsupportedConfigOverrides: runtime.RawExtension{Raw: []byte(`{"garbageCollectionTargetPercentage":"101"}`)},
				},
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// act
			appliedTemplate, err := manageTemplate(
				scenario.template,
				"CaptainAmerica",
				"Piper",
				"v1",
				scenario.operatorSpec)

			// validate
			if err != nil {
				t.Fatal(err)
			}

			if appliedTemplate != scenario.golden {
				t.Fatalf("returned data is different thatn expected. wanted = %v, got %v, the templates was %v", scenario.golden, appliedTemplate, scenario.template)
			}
		})
	}
}

func TestIsRequiredConfigPresentEtcdEndpoints(t *testing.T) {
	configTemplate := `{
		 "servingInfo": {
		   "namedCertificates": [
		     {
		       "certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
		       "keyFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"
		     }
		   ]
		 },
		 "admission": {"pluginConfig": { "network.openshift.io/RestrictedEndpointsAdmission": {}}},
		 "apiServerArguments": {
		   "etcd-servers": %s
		 }
		}
		`

	zeroEtcdEndpoint := makeEtcdEndpointsCM()
	oneEtcdEndpoint := makeEtcdEndpointsCM("ip-10-0-0-1")
	twoEtcdEndpoints := makeEtcdEndpointsCM("ip-10-0-0-1", "ip-10-0-0-2")
	threeEtcdEndpoints := makeEtcdEndpointsCM("ip-10-0-0-1", "ip-10-0-0-2", "ip-10-0-0-3")

	tests := []struct {
		name            string
		etcdServers     string
		etcdEndpointsCM *corev1.ConfigMap
		expectedError   string
		isNotSingleNode bool
	}{
		{
			name:            "nil-storage-urls",
			etcdServers:     "null",
			etcdEndpointsCM: zeroEtcdEndpoint,
			expectedError:   "apiServerArguments.etcd-servers null in config",
		},
		{
			name:            "missing-storage-urls",
			etcdServers:     "[]",
			etcdEndpointsCM: zeroEtcdEndpoint,
			expectedError:   "apiServerArguments.etcd-servers empty in config",
		},
		{
			name:            "empty-string-storage-urls",
			etcdServers:     `""`,
			etcdEndpointsCM: zeroEtcdEndpoint,
			expectedError:   "apiServerArguments.etcd-servers empty in config",
		},
		{
			name:            "missing-etcd-endpoints-configmap",
			etcdServers:     `[ "not-empty" ]`,
			etcdEndpointsCM: &corev1.ConfigMap{},
			isNotSingleNode: true,
			expectedError:   "configmaps \"etcd-endpoints\" not found",
		},
		{
			name:            "bootstrap",
			etcdServers:     `[ "bootstrap" ]`,
			etcdEndpointsCM: zeroEtcdEndpoint,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than two live etcd endpoints: []",
		},
		{
			name:            "bootstrap-one-endpoint",
			etcdServers:     `[ "bootstrap", "ip-10-0-0-1" ]`,
			etcdEndpointsCM: oneEtcdEndpoint,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than two live etcd endpoints: [ip-10-0-0-1]",
		},
		{
			name:            "bootstrap-two-endpoints",
			etcdServers:     `[ "bootstrap", "ip-10-0-0-1", "ip-10-0-0-2" ]`,
			etcdEndpointsCM: twoEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "bootstrap-three-endpoints",
			etcdServers:     `[ "bootstrap", "ip-10-0-0-1", "ip-10-0-0-2", "ip-10-0-0-3" ]`,
			etcdEndpointsCM: threeEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "bootstrap-and-localhost",
			etcdServers:     `[ "bootstrap", "localhost" ]`,
			etcdEndpointsCM: zeroEtcdEndpoint,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than two live etcd endpoints: []",
		},
		{
			name:            "bootstrap-localhost-one-endpoint",
			etcdServers:     `[ "bootstrap", "localhost", "ip-10-0-0-1" ]`,
			etcdEndpointsCM: oneEtcdEndpoint,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than two live etcd endpoints: [ip-10-0-0-1]",
		},
		{
			name:            "bootstrap-localhost-two-endpoints",
			etcdServers:     `[ "bootstrap", "localhost", "ip-10-0-0-1", "ip-10-0-0-2" ]`,
			etcdEndpointsCM: twoEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "bootstrap-localhost-three-endpoints",
			etcdServers:     `[ "bootstrap", "localhost", "ip-10-0-0-1", "ip-10-0-0-2", "ip-10-0-0-3" ]`,
			etcdEndpointsCM: threeEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "one-endpoint",
			etcdServers:     `[ "ip-10-0-0-1" ]`,
			etcdEndpointsCM: oneEtcdEndpoint,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than two live etcd endpoints: [ip-10-0-0-1]",
		},
		{
			name:            "two-endpoints",
			etcdServers:     `[ "ip-10-0-0-1", "ip-10-0-0-2" ]`,
			etcdEndpointsCM: twoEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "three-endpoints",
			etcdServers:     `[ "ip-10-0-0-1", "ip-10-0-0-2", "ip-10-0-0-3" ]`,
			etcdEndpointsCM: threeEtcdEndpoints,
			isNotSingleNode: true,
		},
		{
			name:            "bootstrap-sno",
			etcdServers:     `[ "bootstrap" ]`,
			etcdEndpointsCM: zeroEtcdEndpoint,
			isNotSingleNode: false,
		},
		{
			name:            "one-endpoint-sno",
			etcdServers:     `[ "ip-10-0-0-1" ]`,
			etcdEndpointsCM: oneEtcdEndpoint,
			isNotSingleNode: false,
		},
		{
			name:            "two-endpoints-sno",
			etcdServers:     `[ "ip-10-0-0-1", "ip-10-0-0-2" ]`,
			etcdEndpointsCM: twoEtcdEndpoints,
			isNotSingleNode: false,
		},
		{
			name:            "bootstrap-three-endpoints",
			etcdServers:     `[ "ip-10-0-0-1", "ip-10-0-0-2", "ip-10-0-0-3" ]`,
			etcdEndpointsCM: threeEtcdEndpoints,
			isNotSingleNode: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset(test.etcdEndpointsCM)
			c := TargetConfigController{configMapLister: &configMapLister{client: kubeClient, namespace: etcdEndpointNamespace}}
			config := fmt.Sprintf(configTemplate, test.etcdServers)
			actual := c.isRequiredConfigPresent([]byte(config), test.isNotSingleNode)
			switch {
			case actual == nil && len(test.expectedError) == 0:
			case actual == nil && len(test.expectedError) != 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) == 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) != 0 && !strings.Contains(actual.Error(), test.expectedError):
				t.Fatal(actual)
			}
		})
	}
}

func TestSpecialMergeRules(t *testing.T) {
	mergeRules := map[string]resourcemerge.MergeFunc{
		".apiServerArguments.enable-admission-plugins":  mergeStringSlices,
		".apiServerArguments.disable-admission-plugins": mergeStringSlices,
	}

	configsToMerge := []*kubecontrolplanev1.KubeAPIServerConfig{
		{
			APIServerArguments: map[string]kubecontrolplanev1.Arguments{
				"audit-log-format":          []string{"json"},
				"enable-admission-plugins":  []string{"enabled0"},
				"disable-admission-plugins": []string{"disabled0"},
			},
		},
		{
			APIServerArguments: map[string]kubecontrolplanev1.Arguments{
				"audit-log-format":          []string{"yaml"},
				"enable-admission-plugins":  []string{"enabled1"},
				"disable-admission-plugins": []string{"disabled1"},
			},
		},
		{
			APIServerArguments: map[string]kubecontrolplanev1.Arguments{
				"enable-admission-plugins":  []string{"enabled2"},
				"disable-admission-plugins": []string{"disabled2"},
			},
		},
	}

	configs := make([][]byte, 0, len(configsToMerge))
	for _, cfg := range configsToMerge {
		cfgBytes, err := yaml.Marshal(cfg)
		require.NoError(t, err)
		configs = append(configs, cfgBytes)
	}

	result, _, err := resourcemerge.MergePrunedConfigMap(
		&kubecontrolplanev1.KubeAPIServerConfig{},
		&corev1.ConfigMap{Data: map[string]string{"config.yaml": ""}},
		"config.yaml",
		mergeRules,
		configs...,
	)
	require.NoError(t, err)

	config := &kubecontrolplanev1.KubeAPIServerConfig{}
	err = yaml.Unmarshal([]byte(result.Data["config.yaml"]), config)
	require.NoError(t, err)

	// plugins have special merge rules, therefore slices must be merged
	require.ElementsMatch(t, config.APIServerArguments["enable-admission-plugins"], []string{"enabled0", "enabled1", "enabled2"})
	require.ElementsMatch(t, config.APIServerArguments["disable-admission-plugins"], []string{"disabled0", "disabled1", "disabled2"})

	// audit-log-format does not have any special merge rules, therefore value gets replaced
	require.ElementsMatch(t, config.APIServerArguments["audit-log-format"], []string{"yaml"})
}

func TestMergeStringSlices(t *testing.T) {
	for _, tt := range []struct {
		name        string
		dst         any
		src         any
		expected    any
		expectError bool
	}{
		{
			name:        "dst and src empty",
			dst:         nil,
			src:         nil,
			expected:    nil,
			expectError: false,
		},
		{
			name:        "src empty",
			dst:         []any{"value"},
			src:         nil,
			expected:    []any{"value"},
			expectError: false,
		},
		{
			name:        "dst empty",
			dst:         nil,
			src:         []any{"value"},
			expected:    []any{"value"},
			expectError: false,
		},
		{
			name:        "dst not a slice",
			dst:         "not-a-slice",
			src:         []any{"new-item"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "src not a slice",
			dst:         []any{"existing-item"},
			src:         "not-a-slice",
			expected:    nil,
			expectError: true,
		},
		{
			name:        "dst not a string slice",
			dst:         []any{1, 2, 3},
			src:         []any{"new-item"},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "src not a string slice",
			dst:         []any{"existing-item"},
			src:         []any{1, 2, 3},
			expected:    nil,
			expectError: true,
		},
		{
			name:        "dst and src merged",
			dst:         []any{"existing-item"},
			src:         []any{"new-item"},
			expected:    []string{"existing-item", "new-item"},
			expectError: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := mergeStringSlices(tt.dst, tt.src, "")
			if tt.expectError != (err != nil) {
				t.Errorf("expected error: %v; got %v", tt.expectError, err)
			}

			if !equality.Semantic.DeepEqual(tt.expected, merged) {
				t.Errorf("unexpected merged slice: %s", diff.Diff(tt.expected, merged))
			}

		})
	}
}

func makeEtcdEndpointsCM(endpoints ...string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{}
	cm.Name = etcdEndpointName
	cm.Namespace = etcdEndpointNamespace

	cm.Data = make(map[string]string)
	for i, ep := range endpoints {
		cm.Data[strconv.Itoa(i)] = ep
	}

	return cm
}

type configMapLister struct {
	client    *fake.Clientset
	namespace string
}

var _ corev1listers.ConfigMapNamespaceLister = &configMapLister{}
var _ corev1listers.ConfigMapLister = &configMapLister{}

func (l *configMapLister) List(selector labels.Selector) (ret []*corev1.ConfigMap, err error) {
	list, err := l.client.CoreV1().ConfigMaps(l.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})

	var items []*corev1.ConfigMap
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}

	return items, err
}

func (l *configMapLister) ConfigMaps(namespace string) corev1listers.ConfigMapNamespaceLister {
	return &configMapLister{
		client:    l.client,
		namespace: namespace,
	}
}

func (l *configMapLister) Get(name string) (*corev1.ConfigMap, error) {
	return l.client.CoreV1().ConfigMaps(l.namespace).Get(context.Background(), name, metav1.GetOptions{})
}

func TestManageClientCABundle(t *testing.T) {
	cert1, err := generateTemporaryCertificate()
	require.NoError(t, err)

	cert2, err := generateTemporaryCertificate()
	require.NoError(t, err)

	tests := []struct {
		name               string
		existingConfigMaps []*corev1.ConfigMap
		expectedConfigMap  *corev1.ConfigMap
		expectedChanged    bool
	}{
		{
			name:               "create new client-ca configmap when none exists",
			existingConfigMaps: []*corev1.ConfigMap{},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": "",
				},
			},
			expectedChanged: true,
		},
		{
			name: "one source",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-kubeconfig-client-ca",
						Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "set annotations if missing",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "client-ca",
						Namespace:   operatorclient.TargetNamespace,
						Annotations: map[string]string{},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-kubeconfig-client-ca",
						Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "annotations update",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "client-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							"foo": "bar",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-kubeconfig-client-ca",
						Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
						"foo":                          "bar",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "update existing client-ca configmap when new source appears",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "client-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-apiserver",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-kubeconfig-client-ca",
						Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				// Add a new source that wasn't in the original bundle
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-controller-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert2),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1) + string(cert2),
				},
			},
			expectedChanged: true,
		},
		{
			name: "no changes required",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "client-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-apiserver",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-kubeconfig-client-ca",
						Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// Create existing configmaps
			for _, cm := range test.existingConfigMaps {
				_, err := client.CoreV1().ConfigMaps(cm.Namespace).Create(context.Background(), cm, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			lister := &configMapLister{
				client:    client,
				namespace: "",
			}

			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

			// Call the function under test
			resultConfigMap, changed, err := ManageClientCABundle(context.Background(), lister, client.CoreV1(), recorder)

			// Assert error expectations
			require.NoError(t, err)

			// Assert change expectations
			require.Equal(t, test.expectedChanged, changed, "Expected changed=%v, got changed=%v", test.expectedChanged, changed)

			// Compare with expected configmap
			require.Equal(t, test.expectedConfigMap, resultConfigMap)

			// Verify the configmap exists in the cluster
			storedConfigMap, err := client.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(context.Background(), "client-ca", metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, storedConfigMap)

			// Ensure the returned configmap matches what's stored in the cluster
			require.Equal(t, storedConfigMap, resultConfigMap, "returned configmap should match stored configmap")

			// Verify events were recorded if changes were made
			if test.expectedChanged {
				events := recorder.Events()
				require.NotEmpty(t, events)
			}
		})
	}
}

func TestManageKubeAPIServerCABundle(t *testing.T) {
	cert1, err := generateTemporaryCertificate()
	require.NoError(t, err)

	cert2, err := generateTemporaryCertificate()
	require.NoError(t, err)

	tests := []struct {
		name               string
		existingConfigMaps []*corev1.ConfigMap
		expectedConfigMap  *corev1.ConfigMap
		expectedChanged    bool
	}{
		{
			name:               "create new kube-apiserver-server-ca configmap when none exists",
			existingConfigMaps: []*corev1.ConfigMap{},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": "",
				},
			},
			expectedChanged: true,
		},
		{
			name: "one source",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "loadbalancer-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "set annotations if missing",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "kube-apiserver-server-ca",
						Namespace:   operatorclient.TargetNamespace,
						Annotations: map[string]string{},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "loadbalancer-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "annotations update",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							"foo": "bar",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "loadbalancer-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						"foo":                          "bar",
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "update existing kube-apiserver-server-ca configmap when new source appears",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-apiserver",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "loadbalancer-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				// Add a new source that wasn't in the original bundle
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "localhost-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert2),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1) + string(cert2),
				},
			},
			expectedChanged: true,
		},
		{
			name: "no changes required",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-apiserver",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "loadbalancer-serving-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-server-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-apiserver",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// Create existing configmaps
			for _, cm := range test.existingConfigMaps {
				_, err := client.CoreV1().ConfigMaps(cm.Namespace).Create(context.Background(), cm, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			lister := &configMapLister{
				client:    client,
				namespace: "",
			}

			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

			// Call the function under test
			resultConfigMap, changed, err := manageKubeAPIServerCABundle(context.Background(), lister, client.CoreV1(), recorder)

			// Assert error expectations
			require.NoError(t, err)

			// Assert change expectations
			require.Equal(t, test.expectedChanged, changed, "Expected changed=%v, got changed=%v", test.expectedChanged, changed)

			// Compare with expected configmap
			require.Equal(t, test.expectedConfigMap, resultConfigMap)

			// Verify the configmap exists in the cluster
			storedConfigMap, err := client.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(context.Background(), "kube-apiserver-server-ca", metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, storedConfigMap)

			// Ensure the returned configmap matches what's stored in the cluster
			require.Equal(t, storedConfigMap, resultConfigMap, "returned configmap should match stored configmap")

			// Verify events were recorded if changes were made
			if test.expectedChanged {
				events := recorder.Events()
				require.NotEmpty(t, events)
			}
		})
	}
}

// generateTemporaryCertificate creates a new temporary, self-signed x509 certificate
// and a corresponding RSA private key. The certificate will be valid for 24 hours.
// It returns the PEM-encoded private key and certificate.
func generateTemporaryCertificate() (certPEM []byte, err error) {
	// 1. Generate a new RSA private key
	// We are using a 2048-bit key, which is a common and secure choice.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// 2. Create a template for the certificate
	// This template contains all the details about the certificate.
	certTemplate := x509.Certificate{
		// SerialNumber is a unique number for the certificate.
		// We generate a large random number to ensure uniqueness.
		SerialNumber: big.NewInt(time.Now().Unix()),

		// Subject contains information about the owner of the certificate.
		Subject: pkix.Name{
			Organization: []string{"My Company, Inc."},
			Country:      []string{"US"},
			Province:     []string{"California"},
			Locality:     []string{"San Francisco"},
			CommonName:   "localhost", // Common Name (CN)
		},

		// NotBefore is the start time of the certificate's validity.
		NotBefore: time.Now(),
		// NotAfter is the end time. We set it to 24 hours from now.
		NotAfter: time.Now().Add(24 * time.Hour),

		// KeyUsage defines the purpose of the public key contained in the certificate.
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		// ExtKeyUsage indicates extended purposes (e.g., server/client authentication).
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},

		// BasicConstraintsValid indicates if this is a CA certificate.
		// Since this is a self-signed certificate, we set it to true.
		BasicConstraintsValid: true,
	}

	// 3. Create the certificate
	// x509.CreateCertificate creates a new certificate based on a template.
	// Since this is a self-signed certificate, the parent certificate is the template itself.
	// We use the public key from our generated private key.
	// The final argument is the private key used to sign the certificate.
	certBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}

	// 4. Encode the certificate to the PEM format
	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	return certPEM, nil
}

// TestEnsureKubeAPIServerExtensionAuthenticationCA tests the behavior of ensureKubeAPIServerExtensionAuthenticationCA
func TestEnsureKubeAPIServerExtensionAuthenticationCA(t *testing.T) {
	ctx := context.Background()
	recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

	t.Run("configmap not found (Get error)", func(t *testing.T) {
		// Create a fake client with no configmap in kube-system
		client := fake.NewSimpleClientset()
		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err != nil {
			t.Fatalf("expected nil error when configmap is missing, got: %v", err)
		}
	})

	t.Run("get failure (non-NotFound) returns error", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.Fake.PrependReactor("get", "configmaps", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("conflict")
		})
		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err == nil || !strings.Contains(err.Error(), "conflict") {
			t.Fatalf("expected non-NotFound get error to propagate, got: %v", err)
		}
	})

	t.Run("configmap exists but missing annotations, update succeeds", func(t *testing.T) {
		// Create a configmap without annotations
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "extension-apiserver-authentication",
				Namespace: "kube-system",
			},
		}
		client := fake.NewSimpleClientset(cm)
		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err != nil {
			t.Fatalf("expected nil error after update, got: %v", err)
		}
		updatedCM, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, "extension-apiserver-authentication", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get updated configmap: %v", err)
		}
		if updatedCM.Annotations == nil || updatedCM.Annotations[annotations.OpenShiftComponent] != "kube-apiserver" {
			t.Fatalf("expected annotation not set, got: %v", updatedCM.Annotations)
		}
	})

	t.Run("configmap exists with correct annotations, no update needed", func(t *testing.T) {
		required := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/kube-apiserver/extension-apiserver-authentication-cm.yaml"))

		// Create a configmap with the expected annotation already present
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "extension-apiserver-authentication",
				Namespace:   "kube-system",
				Annotations: required.Annotations,
			},
		}
		client := fake.NewSimpleClientset(cm)
		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err != nil {
			t.Fatalf("expected nil error when annotations are already correct, got: %v", err)
		}

		// Check that client only did one action
		if len(client.Actions()) != 1 {
			t.Fatalf("expected one action, got: %v", client.Actions())
		}
		action := client.Actions()[0]
		if action.GetVerb() != "get" {
			t.Fatalf("expected get action, got: %v", action)
		}
		getAction := action.(clientgotesting.GetAction)
		if getAction.GetName() != "extension-apiserver-authentication" {
			t.Fatalf("expected get action for configmap 'extension-apiserver-authentication', got: %v", getAction)
		}
		if getAction.GetNamespace() != "kube-system" {
			t.Fatalf("expected get action for namespace 'kube-system', got: %v", getAction)
		}
	})

	t.Run("update failure propagates error", func(t *testing.T) {
		// Create a configmap without annotations
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "extension-apiserver-authentication",
				Namespace: "kube-system",
			},
		}
		client := fake.NewSimpleClientset(cm)

		// Inject reactor to simulate update failure
		client.Fake.PrependReactor("update", "configmaps", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("simulated update failure")
		})

		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err == nil || !strings.Contains(err.Error(), "simulated update failure") {
			t.Fatalf("expected update failure error, got: %v", err)
		}
	})

	t.Run("unrelated annotations are not removed", func(t *testing.T) {
		unrelatedAnnotations := map[string]string{
			"unrelated.annotation/key1": "value1",
			"unrelated.annotation/key2": "value2",
		}

		// Create a configmap with unrelated annotations
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "extension-apiserver-authentication",
				Namespace:   "kube-system",
				Annotations: unrelatedAnnotations,
			},
		}
		client := fake.NewSimpleClientset(cm)

		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}

		updatedCM, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, "extension-apiserver-authentication", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get updated configmap: %v", err)
		}

		required := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/kube-apiserver/extension-apiserver-authentication-cm.yaml"))

		expectedAnnotations := map[string]string{}
		for k, v := range required.Annotations {
			expectedAnnotations[k] = v
		}
		for k, v := range unrelatedAnnotations {
			expectedAnnotations[k] = v
		}

		diff := cmp.Diff(expectedAnnotations, updatedCM.Annotations)
		if diff != "" {
			t.Fatalf("expected annotations to match, but got diff:\n%s", diff)
		}
	})

	t.Run("configmap exists with incorrect OpenShiftComponent annotation, update succeeds", func(t *testing.T) {
		// Create a configmap with an incorrect OpenShiftComponent annotation
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "extension-apiserver-authentication",
				Namespace: "kube-system",
				Annotations: map[string]string{
					annotations.OpenShiftComponent: "incorrect-value",
				},
			},
		}
		client := fake.NewSimpleClientset(cm)

		err := ensureKubeAPIServerExtensionAuthenticationCA(ctx, client.CoreV1(), recorder)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}

		updatedCM, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, "extension-apiserver-authentication", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("failed to get updated configmap: %v", err)
		}

		// Verify the OpenShiftComponent annotation is updated to the correct value
		if updatedCM.Annotations == nil || updatedCM.Annotations[annotations.OpenShiftComponent] != "kube-apiserver" {
			t.Errorf("expected annotation %s=kube-apiserver, got: %v", annotations.OpenShiftComponent, updatedCM.Annotations)
		}
	})
}
