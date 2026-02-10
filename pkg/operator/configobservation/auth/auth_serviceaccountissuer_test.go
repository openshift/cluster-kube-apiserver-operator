package auth

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/clock"

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
		expectedJWKSURI        string
	}{
		{
			name:            "no issuer, no previous issuer means we default",
			existingIssuer:  "",
			issuer:          defaultServiceAccountIssuerValue,
			expectedIssuer:  defaultServiceAccountIssuerValue,
			expectedJWKSURI: testLBURI,
		},
		{
			name:            "no issuer, previous issuer",
			existingIssuer:  "https://example.com",
			issuer:          defaultServiceAccountIssuerValue,
			expectedIssuer:  defaultServiceAccountIssuerValue,
			expectedJWKSURI: testLBURI,
			expectedChange:  true,
		},
		{
			name:            "issuer set, no previous issuer",
			existingIssuer:  "",
			issuer:          "https://example.com",
			expectedIssuer:  "https://example.com",
			expectedJWKSURI: "https://example.com/openid/v1/jwks",
			expectedChange:  true,
		},
		{
			name:                   "previous issuer was default, new is custom value",
			existingIssuer:         defaultServiceAccountIssuerValue,
			issuer:                 "https://example.com",
			expectedIssuer:         "https://example.com",
			trustedIssuers:         []string{defaultServiceAccountIssuerValue},
			expectedTrustedIssuers: []string{defaultServiceAccountIssuerValue},
			expectedJWKSURI:        "https://example.com/openid/v1/jwks",
			expectedChange:         true,
		},
		{
			name:            "issuer set, previous issuer same",
			existingIssuer:  "https://example.com",
			issuer:          "https://example.com",
			expectedIssuer:  "https://example.com",
			expectedJWKSURI: "https://example.com/openid/v1/jwks",
		},
		{
			name:                   "issuer set, previous issuer and trusted issuers same",
			existingIssuer:         "https://example.com",
			issuer:                 "https://example.com",
			trustedIssuers:         []string{"https://trusted.example.com"},
			expectedIssuer:         "https://example.com",
			expectedTrustedIssuers: []string{"https://trusted.example.com"},
			expectedJWKSURI:        "https://example.com/openid/v1/jwks",
		},
		{
			name:            "issuer set, previous issuer different",
			existingIssuer:  "https://example.com",
			issuer:          "https://example2.com",
			expectedIssuer:  "https://example2.com",
			expectedJWKSURI: "https://example2.com/openid/v1/jwks",
			expectedChange:  true,
		},
		{
			name:            "auth getter error",
			existingIssuer:  "https://example2.com",
			issuer:          "https://example.com",
			authError:       expectedErrAuth,
			expectedIssuer:  "https://example2.com",
			expectedJWKSURI: "https://example2.com/openid/v1/jwks", // preserve existing
		},
		{
			name:            "infra getter error",
			existingIssuer:  defaultServiceAccountIssuerValue,
			issuer:          defaultServiceAccountIssuerValue,
			infraError:      expectedErrInfra,
			expectedIssuer:  defaultServiceAccountIssuerValue,
			expectedJWKSURI: testLBURI, // no previous + infra error -> do NOT set JWKS
		},
		{
			name:            "default issuer, no previous issuer, infra getter error",
			existingIssuer:  "",
			issuer:          defaultServiceAccountIssuerValue,
			expectedIssuer:  "",
			infraError:      expectedErrInfra,
			expectedJWKSURI: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest", clock.RealClock{})
			newConfig, errs := observedConfig(
				unstructuredAPIConfigForIssuer(t, tc.existingIssuer, tc.trustedIssuers),
				func(_ string) (*operatorv1.KubeAPIServer, error) {
					return kasStatusForIssuer(tc.issuer, tc.trustedIssuers...), tc.authError
				},
				func(_ string) (*configv1.Infrastructure, error) {
					return &configv1.Infrastructure{
						Status: configv1.InfrastructureStatus{
							APIServerURL: "https://lb.example.com",
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

			// Check that JWKS URI is correctly set
			uri, ok := unmarshalledConfig.APIServerArguments["service-account-jwks-uri"]
			if tc.expectedJWKSURI != "" {
				require.True(t, ok, "expected service-account-jwks-uri to be set")
				require.Equal(t, kubecontrolplanev1.Arguments{tc.expectedJWKSURI}, uri)
			} else {
				require.False(t, ok, "did not expect service-account-jwks-uri to be set")
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
	// Determine JWKS URI dynamically
	var jwksURI string
	switch issuer {
	case defaultServiceAccountIssuerValue:
		jwksURI = testLBURI // default issuer uses APIServerURL
		args["service-account-jwks-uri"] = kubecontrolplanev1.Arguments{jwksURI}
	case "":
		jwksURI = ""
	default:
		// custom issuer
		args["service-account-jwks-uri"] = kubecontrolplanev1.Arguments{
			issuer + "/openid/v1/jwks",
		}
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
