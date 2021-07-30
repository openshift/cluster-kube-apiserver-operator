package e2e_sno_disruptive

import (
	"testing"

	"github.com/stretchr/testify/require"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	libgotest "github.com/openshift/library-go/test/library"

	"k8s.io/client-go/kubernetes"
)

type clientSet struct {
	Infra    configv1.InfrastructureInterface
	Operator operatorv1client.KubeAPIServerInterface
	Kube     kubernetes.Interface
}

func getClients(t testing.TB) clientSet {
	t.Helper()

	kubeConfig, err := libgotest.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	configClient, err := configv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return clientSet{Infra: configClient.ConfigV1().Infrastructures(), Operator: operatorClient.KubeAPIServers(), Kube: kubeClient}
}
