package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ghodss/yaml"
	g "github.com/onsi/ginkgo/v2"
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

var clusterDefaultCORSALlowedOriginsGinkgo = []string{
	`//127\.0\.0\.1(:|$)`,
	`//localhost(:|$)`,
}

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestAdditionalCORSAllowedOrigins [Serial][Timeout:20m]", func() {
		TestAdditionalCORSAllowedOrigins(g.GinkgoTB())
	})
})

func TestAdditionalCORSAllowedOrigins(t testing.TB) {
	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	kubeAPIServerOperatorClient := operatorClient.KubeAPIServers()

	// Check the cluster defaults
	defaultConfig := getKubeAPIServerConfigOrFailGinkgo(t, kubeAPIServerOperatorClient)
	assert.Equal(t, clusterDefaultCORSALlowedOriginsGinkgo, defaultConfig)

	t.Logf("Cluster default CORSAllowedOrigins: %v", defaultConfig)

	testCases := []struct {
		name               string
		additionalCORS     []string
		expectedConfigCORS []string
	}{
		{
			name:           "SingleDomain",
			additionalCORS: []string{"//valid.domain.com(:|$)"},
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//localhost(:|$)`,
				`//valid.domain.com(:|$)`,
			},
		},
		{
			name:           "MultipleDomains",
			additionalCORS: []string{"//something.*.now(:|$)", "//domain.foreign.it(:|$)"},
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//domain.foreign.it(:|$)`,
				`//localhost(:|$)`,
				`//something.*.now(:|$)`,
			},
		},
		{
			name: "Default",
			expectedConfigCORS: []string{
				`//127\.0\.0\.1(:|$)`,
				`//localhost(:|$)`,
			},
		},
	}

	for i, tc := range testCases {
		t.Logf("Running test case: %s", tc.name)
		updateAPIServerClusterConfigSpecGinkgo(configClient, func(apiserver *configv1.APIServer) {
			apiserver.Spec.AdditionalCORSAllowedOrigins = tc.additionalCORS
		})

		var currentCORS []string
		err = wait.PollImmediate(time.Second, wait.ForeverTestTimeout, func() (bool, error) {
			currentCORS = getKubeAPIServerConfigOrFailGinkgo(t, kubeAPIServerOperatorClient)
			if !equality.Semantic.DeepEqual(currentCORS, tc.expectedConfigCORS) {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			t.Errorf("test %d (%s) failed: expected %#v, got %#v", i, tc.name, tc.expectedConfigCORS, currentCORS)
		}
	}

}

func getKubeAPIServerConfigOrFailGinkgo(t testing.TB, operatorClient operatorclient.KubeAPIServerInterface) []string {
	operatorConfig, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	var unstructuredConfig map[string]interface{}
	err = yaml.Unmarshal(operatorConfig.Spec.ObservedConfig.Raw, &unstructuredConfig)
	require.NoError(t, err)
	cors, _, err := unstructured.NestedStringSlice(unstructuredConfig, "corsAllowedOrigins")
	require.NoError(t, err)
	return cors
}
