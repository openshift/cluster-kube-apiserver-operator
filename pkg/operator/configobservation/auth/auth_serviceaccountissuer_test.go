package auth

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
)

func TestObservedConfig(t *testing.T) {
	existingConfig := unstructuredAPIConfigForIssuer(t, "https://example.com")
	authConfig := authConfigForIssuer("https://example.com")
	expectedErr := fmt.Errorf("foo")
	newConfig, errs := observedConfig(
		existingConfig,
		func(_ string) (*configv1.Authentication, error) {
			return authConfig, expectedErr
		},
	)

	// Check that errors are passed through
	require.Len(t, errs, 1)
	require.Equal(t, errs[0], expectedErr)

	// The observed config must unmarshall to
	// KubeAPIServerConfig without error.
	unstructuredConfig := unstructured.Unstructured{
		Object: newConfig,
	}
	jsonConfig, err := unstructuredConfig.MarshalJSON()
	require.NoError(t, err)
	unmarshalledConfig := &kubecontrolplanev1.KubeAPIServerConfig{}
	require.NoError(t, json.Unmarshal(jsonConfig, unmarshalledConfig))
	expectedConfig := apiConfigForIssuer("https://example.com")
	require.Equal(t, expectedConfig, unmarshalledConfig)
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
	return &kubecontrolplanev1.KubeAPIServerConfig{
		APIServerArguments: map[string]kubecontrolplanev1.Arguments{
			"service-account-issuer": {
				issuer,
			},
		},
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
