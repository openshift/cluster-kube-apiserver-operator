package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestMetricsRegistration(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)

	// get list of operator pods
	pods, err := kubeClient.CoreV1().Pods(operatorclient.OperatorNamespace).List(metav1.ListOptions{})
	require.NoError(t, err)

	// we just care about the one we expect
	require.GreaterOrEqual(t, len(pods.Items), 1)
	pod := pods.Items[0]

	metrics, err := test.GetMetricsForPod(t, kubeConfig, &pod, 8443)
	require.NoError(t, err)
	t.Logf("Retrieved %d metrics.", len(metrics))
	if len(metrics) == 0 {
		t.Fatal("No metrics retrieved.")
	}
}
