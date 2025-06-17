package targetconfigcontroller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/ghodss/yaml"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/stretchr/testify/require"
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
				t.Errorf("unexpected merged slice: %s", diff.ObjectReflectDiff(tt.expected, merged))
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
