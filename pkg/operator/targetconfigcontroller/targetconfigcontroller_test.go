package targetconfigcontroller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
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
			actual := isRequiredConfigPresent([]byte(test.config))
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

func TestManageTemplate(t *testing.T) {
	scenarios := []struct {
		name         string
		template     string
		golden       string
		operatorSpec *operatorv1.StaticPodOperatorSpec
		platformType configv1.PlatformType
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
			name:         "the GracefulTerminationDuration is extended due to a known AWS issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804a",
			template:     "{{.Image}}, {{.OperatorImage}}, {{.Verbosity}}, {{.GracefulTerminationDuration}}",
			golden:       "CaptainAmerica, Piper,  -v=2, 275",
			operatorSpec: &operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{}},
			platformType: configv1.AWSPlatformType,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			infrastructureIndexer.Add(&configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}, Spec: configv1.InfrastructureSpec{PlatformSpec: configv1.PlatformSpec{Type: scenario.platformType}}})
			infrastructureLister := configlistersv1.NewInfrastructureLister(infrastructureIndexer)

			// act
			appliedTemplate, err := manageTemplate(
				scenario.template,
				"CaptainAmerica",
				"Piper",
				infrastructureLister,
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
