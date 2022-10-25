package auth

import (
	"encoding/json"
	"fmt"
	operatorv1 "github.com/openshift/api/operator/v1"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	"github.com/openshift/library-go/pkg/operator/events"
)

const testLBURI = "https://lb.example.com/openid/v1/jwks"

func TestObservedConfig(t *testing.T) {
	expectedErrAuth := fmt.Errorf("foo")
	expectedErrInfra := fmt.Errorf("bar")

	for _, tc := range []struct {
		name                   string
		issuer                 string
		trustedIssuers         []string
		existingIssuer         string
		authError              error
		infraError             error
		expectedIssuer         string
		expectedTrustedIssuers []string
		expectedChange         bool
		expectInternalJWKI     bool
	}{
		{
			name:               "no issuer, no previous issuer means we default",
			existingIssuer:     "",
			issuer:             defaultServiceAccountIssuerValue,
			expectedIssuer:     defaultServiceAccountIssuerValue,
			expectInternalJWKI: true,
		},
		{
			name:               "no issuer, previous issuer",
			existingIssuer:     "https://example.com",
			issuer:             defaultServiceAccountIssuerValue,
			expectedIssuer:     defaultServiceAccountIssuerValue,
			expectInternalJWKI: true,
			expectedChange:     true,
		},
		{
			name:           "issuer set, no previous issuer",
			existingIssuer: "",
			issuer:         "https://example.com",
			expectedIssuer: "https://example.com",
			expectedChange: true,
		},
		{
			name:                   "previous issuer was default, new is custom value",
			existingIssuer:         defaultServiceAccountIssuerValue,
			issuer:                 "https://example.com",
			expectedIssuer:         "https://example.com",
			trustedIssuers:         []string{defaultServiceAccountIssuerValue},
			expectedTrustedIssuers: []string{defaultServiceAccountIssuerValue},
			expectInternalJWKI:     false, // this proves we remove the internal api LB when custom value is set
			expectedChange:         true,
		},
		{
			name:           "issuer set, previous issuer same",
			existingIssuer: "https://example.com",
			issuer:         "https://example.com",
			expectedIssuer: "https://example.com",
		},
		{
			name:                   "issuer set, previous issuer and trusted issuers same",
			existingIssuer:         "https://example.com",
			issuer:                 "https://example.com",
			trustedIssuers:         []string{"https://trusted.example.com"},
			expectedIssuer:         "https://example.com",
			expectedTrustedIssuers: []string{"https://trusted.example.com"},
		},
		{
			name:           "issuer set, previous issuer different",
			existingIssuer: "https://example.com",
			issuer:         "https://example2.com",
			expectedIssuer: "https://example2.com",
			expectedChange: true,
		},
		{
			name:           "auth getter error",
			existingIssuer: "https://example2.com",
			issuer:         "https://example.com",
			authError:      expectedErrAuth,
			expectedIssuer: "https://example2.com",
		},
		{
			name:               "infra getter error",
			existingIssuer:     defaultServiceAccountIssuerValue,
			issuer:             defaultServiceAccountIssuerValue,
			infraError:         expectedErrInfra,
			expectedIssuer:     defaultServiceAccountIssuerValue,
			expectInternalJWKI: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest")

			newConfig, errs := observedConfig(
				unstructuredAPIConfigForIssuer(t, tc.existingIssuer, tc.trustedIssuers),
				func(_ string) (*operatorv1.KubeAPIServer, error) {
					return kasStatusForIssuer(tc.issuer, tc.trustedIssuers...), tc.authError
				},
				func(_ string) (*configv1.Infrastructure, error) {
					return &configv1.Infrastructure{
						Status: configv1.InfrastructureStatus{
							APIServerInternalURL: "https://lb.example.com",
						},
					}, tc.infraError
				},
				testRecorder,
			)

			var expectedConfig *kubecontrolplanev1.KubeAPIServerConfig
			if tc.authError == nil && tc.infraError == nil {
				require.Len(t, errs, 0)
			}
			expectedConfig = apiConfigForIssuer(tc.expectedIssuer, tc.expectedTrustedIssuers)

			// Check that errors are passed through
			if tc.authError != nil {
				require.Contains(t, errs, expectedErrAuth)
			}
			if tc.infraError != nil {
				require.Contains(t, errs, expectedErrInfra)
			}

			// The observed config must unmarshall to
			// KubeAPIServerConfig without error.
			unstructuredConfig := unstructured.Unstructured{
				Object: newConfig,
			}
			jsonConfig, err := unstructuredConfig.MarshalJSON()
			require.NoError(t, err)
			unmarshalledConfig := &kubecontrolplanev1.KubeAPIServerConfig{
				TypeMeta: metav1.TypeMeta{
					Kind: "KubeAPIServerConfig",
				},
			}

			require.NoError(t, json.Unmarshal(jsonConfig, unmarshalledConfig))
			uri, ok := unmarshalledConfig.APIServerArguments["service-account-jwks-uri"]
			if tc.expectInternalJWKI {
				if !ok {
					t.Errorf("expected service-account-jwks-uri to be set, it is not")
				} else {
					require.Equal(t, uri, kubecontrolplanev1.Arguments{testLBURI})
				}
			}
			if !tc.expectInternalJWKI && ok {
				t.Errorf("expected no service-account-jwks-uri to be set, it is %+v", uri.String())
			}
			require.Equal(t, expectedConfig, unmarshalledConfig, cmp.Diff(expectedConfig, unmarshalledConfig))
			require.True(t, tc.expectedChange == (len(testRecorder.Events()) > 0))
		})
	}
}

func kasStatusForIssuer(active string, trustedIssuers ...string) *operatorv1.KubeAPIServer {
	if len(active) == 0 {
		return &operatorv1.KubeAPIServer{
			Status: operatorv1.KubeAPIServerStatus{},
		}
	}
	status := []operatorv1.ServiceAccountIssuerStatus{
		{
			Name: active,
		},
	}
	for i := range trustedIssuers {
		status = append(status, operatorv1.ServiceAccountIssuerStatus{
			Name:           trustedIssuers[i],
			ExpirationTime: &metav1.Time{Time: time.Now().Add(12 * time.Hour)},
		})
	}
	return &operatorv1.KubeAPIServer{
		Status: operatorv1.KubeAPIServerStatus{
			ServiceAccountIssuers: status,
		},
	}
}

func apiConfigForIssuer(issuer string, trustedIssuers []string) *kubecontrolplanev1.KubeAPIServerConfig {
	args := map[string]kubecontrolplanev1.Arguments{
		"service-account-issuer": append([]string{issuer}, trustedIssuers...),
		"api-audiences":          append([]string{issuer}, trustedIssuers...),
	}
	if issuer == defaultServiceAccountIssuerValue {
		//delete(args, "service-account-issuer")
		//delete(args, "api-audiences")
		args["service-account-jwks-uri"] = kubecontrolplanev1.Arguments{testLBURI}
	}

	return &kubecontrolplanev1.KubeAPIServerConfig{
		TypeMeta: metav1.TypeMeta{
			Kind: "KubeAPIServerConfig",
		},
		APIServerArguments: args,
	}
}

// unstructuredAPIConfigForIssuer round-trips through the golang type
// to ensure the input to the function under test will match what will
// be received at runtime.
func unstructuredAPIConfigForIssuer(t *testing.T, issuer string, trustedIssuers []string) map[string]interface{} {
	config := apiConfigForIssuer(issuer, trustedIssuers)
	// Unmarshaling to unstructured requires explicitly setting kind
	config.TypeMeta = metav1.TypeMeta{
		Kind: "KubeAPIServerConfig",
	}
	marshalledConfig, err := json.Marshal(config)
	require.NoError(t, err)
	unstructuredObj := &unstructured.Unstructured{}
	require.NoError(t, json.Unmarshal(marshalledConfig, unstructuredObj))
	return unstructuredObj.Object
}
