package targetconfigcontroller

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/client-go/kubernetes/scheme"
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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isRequiredConfigPresent([]byte(test.config), false)
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
	tests := []struct {
		name            string
		etcdServers     string
		expectedError   string
		isNotSingleNode bool
	}{
		{
			name:          "nil-storage-urls",
			etcdServers:   "null",
			expectedError: "apiServerArguments.etcd-servers null in config",
		},
		{
			name:          "missing-storage-urls",
			etcdServers:   "[]",
			expectedError: "apiServerArguments.etcd-servers empty in config",
		},
		{
			name:          "empty-string-storage-urls",
			etcdServers:   `""`,
			expectedError: "apiServerArguments.etcd-servers empty in config",
		},
		{
			name:            "one-etcd-server",
			etcdServers:     `[ "val" ]`,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than three endpoints",
		},
		{
			name:            "one-etcd-server-sno",
			etcdServers:     `[ "val" ]`,
			isNotSingleNode: false,
		},
		{
			name:            "two-etcd-servers",
			etcdServers:     `[ "val1", "val2" ]`,
			isNotSingleNode: true,
			expectedError:   "apiServerArguments.etcd-servers has less than three endpoints",
		},
		{
			name:            "two-etcd-servers-sno",
			etcdServers:     `[ "val1", "val2" ]`,
			isNotSingleNode: false,
		},
		{
			name:            "three-etcd-servers",
			etcdServers:     `[ "val1", "val2", "val3" ]`,
			isNotSingleNode: true,
		},
		{
			name:            "three-etcd-servers-sno",
			etcdServers:     `[ "val1", "val2", "val3" ]`,
			isNotSingleNode: false,
		},
		{
			name:            "four-etcd-servers",
			etcdServers:     `[ "val1", "val2", "val3", "val4" ]`,
			isNotSingleNode: true,
		},
		{
			name:            "four-etcd-servers-sno",
			etcdServers:     `[ "val1", "val2", "val3", "val4" ]`,
			isNotSingleNode: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fmt.Sprintf(configTemplate, test.etcdServers)
			actual := isRequiredConfigPresent([]byte(config), test.isNotSingleNode)
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
