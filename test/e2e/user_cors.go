package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"

	g "github.com/onsi/ginkgo/v2"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial] validates CORS with single additional origin", func() {
		testCORSWithSingleOrigin(g.GinkgoTB())
	})

	g.It("[Operator][Serial] validates CORS with multiple additional origins", func() {
		testCORSWithMultipleOrigins(g.GinkgoTB())
	})

	g.It("[Operator][Serial] validates CORS clearing to defaults", func() {
		testCORSClearToDefaults(g.GinkgoTB())
	})
})

func testCORSWithSingleOrigin(t testing.TB) {
	additionalCORS := []string{"//valid.domain.com(:|$)"}
	expectedConfigCORS := []string{
		`//127\.0\.0\.1(:|$)`,
		`//localhost(:|$)`,
		`//valid.domain.com(:|$)`,
	}
	testCORSConfiguration(t, additionalCORS, expectedConfigCORS)
}

func testCORSWithMultipleOrigins(t testing.TB) {
	additionalCORS := []string{"//something.*.now(:|$)", "//domain.foreign.it(:|$)"}
	expectedConfigCORS := []string{
		`//127\.0\.0\.1(:|$)`,
		`//domain.foreign.it(:|$)`,
		`//localhost(:|$)`,
		`//something.*.now(:|$)`,
	}
	testCORSConfiguration(t, additionalCORS, expectedConfigCORS)
}

func testCORSClearToDefaults(t testing.TB) {
	var additionalCORS []string // nil
	expectedConfigCORS := []string{
		`//127\.0\.0\.1(:|$)`,
		`//localhost(:|$)`,
	}
	testCORSConfiguration(t, additionalCORS, expectedConfigCORS)
}

// testCORSConfiguration is a common test function that sets the additional CORS
// origins and verifies that the expected CORS configuration is applied.
func testCORSConfiguration(t testing.TB, additionalCORS, expectedConfigCORS []string) {
	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	kubeAPIServerOperatorClient := operatorClient.KubeAPIServers()

	updateAPIServerClusterConfigSpec(configClient, func(apiserver *configv1.APIServer) {
		apiserver.Spec.AdditionalCORSAllowedOrigins = additionalCORS
	})

	var currentCORS []string
	err = wait.PollImmediate(time.Second, wait.ForeverTestTimeout, func() (bool, error) {
		currentCORS = getKubeAPIServerConfigOrFail(t, kubeAPIServerOperatorClient)
		if !equality.Semantic.DeepEqual(currentCORS, expectedConfigCORS) {
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err, "expected %#v, got %#v", expectedConfigCORS, currentCORS)
}

func getKubeAPIServerConfigOrFail(t testing.TB, operatorClient operatorclient.KubeAPIServerInterface) []string {
	operatorConfig, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	var unstructuredConfig map[string]interface{}
	err = yaml.Unmarshal(operatorConfig.Spec.ObservedConfig.Raw, &unstructuredConfig)
	require.NoError(t, err)
	cors, _, err := unstructured.NestedStringSlice(unstructuredConfig, "corsAllowedOrigins")
	require.NoError(t, err)
	return cors
}
