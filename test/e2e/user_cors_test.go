package e2e

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

var clusterDefaultCORSALlowedOrigins = []string{
	`//127\.0\.0\.1(:|$)`,
	`//localhost(:|$)`,
}

func TestAdditionalCORSAllowedOrigins(t *testing.T) {
	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	kubeAPIServerOperatorClient := operatorClient.KubeAPIServers()

	// Check the cluster defaults
	defaultConfig := getKubeAPIServerConfigOrFail(t, kubeAPIServerOperatorClient)
	assert.Equal(t, clusterDefaultCORSALlowedOrigins, defaultConfig)

	t.Logf("Cluster default CORSAllowedOrigins: %v", defaultConfig)

	testCases := []struct {
		additionalCORS     []string
		expectedConfigCORS []string
	}{
		{
			additionalCORS: []string{"//valid.domain.com(:|$)"},
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//localhost(:|$)`,
				`//valid.domain.com(:|$)`,
			},
		},
		{
			additionalCORS: []string{"//something.*.now(:|$)", "//domain.foreign.it(:|$)"},
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//domain.foreign.it(:|$)`,
				`//localhost(:|$)`,
				`//something.*.now(:|$)`,
			},
		},
		{
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//localhost(:|$)`,
			},
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%v", tc.additionalCORS), func(t *testing.T) {
			updateAPIServerClusterConfigSpec(configClient, func(apiserver *configv1.APIServer) {
				apiserver.Spec.AdditionalCORSAllowedOrigins = tc.additionalCORS
			})

			var currentCORS []string
			err = wait.PollImmediate(time.Second, wait.ForeverTestTimeout, func() (bool, error) {
				currentCORS = getKubeAPIServerConfigOrFail(t, kubeAPIServerOperatorClient)
				if !equality.Semantic.DeepEqual(currentCORS, tc.expectedConfigCORS) {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				t.Errorf("test %d failed: expected %#v, got %#v", i, tc.expectedConfigCORS, currentCORS)
			}
		})
	}

}

func getKubeAPIServerConfigOrFail(t *testing.T, operatorClient operatorclient.KubeAPIServerInterface) []string {
	operatorConfig, err := operatorClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)

	var unstructuredConfig map[string]interface{}
	err = yaml.Unmarshal(operatorConfig.Spec.ObservedConfig.Raw, &unstructuredConfig)
	require.NoError(t, err)
	cors, _, err := unstructured.NestedStringSlice(unstructuredConfig, "corsAllowedOrigins")
	require.NoError(t, err)
	return cors
}
