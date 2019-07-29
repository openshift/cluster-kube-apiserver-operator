package e2e

import (
	"testing"

	operatorclientv1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFeatureGatesUpgradeable(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	operatorClient := operatorclientv1.NewForConfigOrDie(kubeConfig)

	// if the condition is true, then we're active.  The unit tests confirm it goes false.
	operatorState, err := operatorClient.KubeAPIServers().Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionTrue(operatorState.Status.Conditions, "FeatureGatesUpgradeable"))
}
