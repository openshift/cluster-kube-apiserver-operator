package auth

import (
	"encoding/json"
	"fmt"
	"testing"

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
		name           string
		issuer         string
		existingIssuer string
		authError      error
		infraError     error
		expectedIssuer string
		expectedChange bool
	}{
		{
			name:           "no issuer, no previous issuer",
			existingIssuer: "",
			issuer:         "",
			expectedIssuer: "",
		},
		{
			name:           "no issuer, previous issuer set",
			existingIssuer: "https://example.com",
			issuer:         "",
			expectedIssuer: "",
			expectedChange: true,
		},
		{
			name:           "issuer set, no previous issuer",
			existingIssuer: "",
			issuer:         "https://example.com",
			expectedIssuer: "https://example.com",
			expectedChange: true,
		},
		{
			name:           "issuer set, previous issuer same",
			existingIssuer: "https://example.com",
			issuer:         "https://example.com",
			expectedIssuer: "https://example.com",
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
			name:           "infra getter error",
			existingIssuer: "https://example.com",
			issuer:         "",
			infraError:     expectedErrInfra,
			expectedIssuer: "https://example.com",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testRecorder := events.NewInMemoryRecorder("SAIssuerTest")

			newConfig, errs := observedConfig(
				unstructuredAPIConfigForIssuer(t, tc.existingIssuer),
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
				testRecorder,
			)

			var expectedConfig *kubecontrolplanev1.KubeAPIServerConfig
			if tc.authError == nil && tc.infraError == nil {
				require.Len(t, errs, 0)
			}
			expectedConfig = apiConfigForIssuer(tc.expectedIssuer)

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
			require.True(t, tc.expectedChange == (len(testRecorder.Events()) > 0))
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
