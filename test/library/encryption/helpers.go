package encryption

import (
	"testing"

	"github.com/stretchr/testify/require"

	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func GetOperator(t testing.TB) operatorv1client.KubeAPIServerInterface {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return operatorClient.KubeAPIServers()
}
