package targetconfigcontroller

import (
	"strings"
	"testing"
)

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
			name: "nil-namedCertificates",
			config: `{
		 "servingInfo": {
		   "namedCertificates": null
		 }
		}
		`,
			expectedError: "servingInfo.namedCertificates null in config",
		},
		{
			name: "missing-namedCertificates",
			config: `{
		 "servingInfo": {
		   "namedCertificates": []
		 }
		}
		`,
			expectedError: "servingInfo.namedCertificates empty in config",
		},
		{
			name: "empty-string-namedCertificates",
			config: `{
  "servingInfo": {
    "namedCertificates": ""
  }
}
`,
			expectedError: "servingInfo.namedCertificates empty in config",
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
