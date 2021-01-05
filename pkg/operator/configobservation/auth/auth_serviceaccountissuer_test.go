package auth

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
)

const testLBURI = "https://lb.example.com/openid/v1/jwks"

func TestObservedConfig(t *testing.T) {
	existingConfig := unstructuredAPIConfigForIssuer(t, "https://example.com")
	expectExistingConfig := apiConfigForIssuer("https://example.com")
	expectedErrAuth := fmt.Errorf("foo")
	expectedErrInfra := fmt.Errorf("bar")

	for _, tc := range []struct {
		name       string
		issuer     string
		authError  error
		infraError error
	}{
		{
			name:   "no issuer",
			issuer: "",
		},
		{
			name:   "issuer set",
			issuer: "https://example.com",
		},
		{
			name:      "auth getter error",
			issuer:    "https://example.com",
			authError: expectedErrAuth,
		},
		{
			name:       "infra getter error",
			issuer:     "",
			infraError: expectedErrInfra,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newConfig, errs := observedConfig(
				existingConfig,
				func(_ string) (*configv1.Authentication, error) {
					return authConfigForIssuer(tc.issuer), tc.authError
				},
				func(_ string) (*configv1.Infrastructure, error) {
					return &configv1.Infrastructure{
						Status: configv1.InfrastructureStatus{
							APIServerInternalURL: "https://lb.example.com",
						},
					}, tc.infraError
				},
			)

			var expectedConfig *kubecontrolplanev1.KubeAPIServerConfig
			if tc.authError == nil && tc.infraError == nil {
				require.Len(t, errs, 0)
				expectedConfig = apiConfigForIssuer(tc.issuer)
			} else {
				expectedConfig = expectExistingConfig
			}

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
			require.Equal(t, expectedConfig, unmarshalledConfig, cmp.Diff(expectedConfig, unmarshalledConfig))
		})
	}
}

func TestIssuerFromUnstructuredConfig(t *testing.T) {
	testCases := map[string]struct {
		issuer         string
		expectedIssuer string
		errorExpected  bool
	}{
		"Return empty if missing": {},
		"Return error if has a colon but is not a url": {
			issuer:        "invalid : issuer",
			errorExpected: true,
		},
		"Return valid issuer": {
			issuer:         "valid-issuer",
			expectedIssuer: "valid-issuer",
		},
	}
	for testName, tc := range testCases {
		t.Run(testName, func(t *testing.T) {
			config := unstructuredAPIConfigForIssuer(t, tc.issuer)
			issuer, errs := issuerFromUnstructuredConfig(config)
			require.Equal(t, tc.expectedIssuer, issuer)
			if tc.errorExpected {
				require.Len(t, errs, 1)
			} else if len(errs) > 0 {
				require.NoError(t, errs[0])
			}
			require.Equal(t, tc.expectedIssuer, issuer)
		})
	}
}

func TestObservedIssuer(t *testing.T) {
	testCases := map[string]struct {
		previousIssuer string
		authIssuer     string
		accessorError  error
		expectedIssuer string
		errorExpected  bool
	}{
		"Return empty if config resource is missing": {
			accessorError: apierrors.NewNotFound(schema.GroupResource{}, ""),
		},
		"Return existing config if error on config resource access": {
			previousIssuer: "https://example.com",
			accessorError:  fmt.Errorf("Random error"),
			expectedIssuer: "https://example.com",
			errorExpected:  true,
		},
		"Return existing config if the issuer has a colon but is not a url": {
			previousIssuer: "https://example.com",
			authIssuer:     "invalid : issuer",
			expectedIssuer: "https://example.com",
			errorExpected:  true,
		},
		"Return updated config if the new issuer is valid": {
			previousIssuer: "https://example.com",
			authIssuer:     "new-issuer",
			expectedIssuer: "new-issuer",
		},
	}
	for testName, tc := range testCases {
		t.Run(testName, func(t *testing.T) {
			issuer, errs := observedIssuer(
				unstructuredAPIConfigForIssuer(t, tc.previousIssuer),
				func(_ string) (*configv1.Authentication, error) {
					if tc.accessorError != nil {
						return nil, tc.accessorError
					}
					return authConfigForIssuer(tc.authIssuer), nil
				},
			)
			if tc.errorExpected {
				require.Len(t, errs, 1)
			} else {
				if len(errs) > 0 {
					require.NoError(t, errs[0])
				}
				require.Equal(t, tc.expectedIssuer, issuer)
			}
		})
	}
}

func authConfigForIssuer(issuer string) *configv1.Authentication {
	return &configv1.Authentication{
		Spec: configv1.AuthenticationSpec{
			ServiceAccountIssuer: issuer,
		},
	}
}

func apiConfigForIssuer(issuer string) *kubecontrolplanev1.KubeAPIServerConfig {
	args := map[string]kubecontrolplanev1.Arguments{
		"service-account-issuer": {
			issuer,
		},
	}
	if len(issuer) == 0 {
		delete(args, "service-account-issuer")
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
func unstructuredAPIConfigForIssuer(t *testing.T, issuer string) map[string]interface{} {
	config := apiConfigForIssuer(issuer)
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
