package e2e

import (
	"context"
	"testing"

	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/metrics"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/stretchr/testify/require"
)

func TestGetNodeAndPodSummary(t *testing.T) {
	config, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	clientset, err := metricsclient.NewForConfig(config)
	require.NoError(t, err)

	metricsClient := metrics.NewClient(clientset)
	require.NotNil(t, metricsClient)

	summary, err := metricsClient.GetNodeAndPodSummary(context.TODO())
	require.NoError(t, err)
	require.True(t, summary.TotalNodes > 1)
	require.True(t, summary.TotalPods > 1)

	t.Logf("TestGetNodeAndPodSummary: metrics summary=%s", summary.String())
}
