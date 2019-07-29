package e2e

import (
	"testing"

	"github.com/openshift/library-go/pkg/operator/v1helpers"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"

	"github.com/stretchr/testify/require"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestFeatureGatesUpgradeable(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	operatorClient, _, err := genericoperatorclient.NewStaticPodOperatorClient(kubeConfig, operatorv1.GroupVersion.WithResource("kubeapiservers"))
	require.NoError(t, err)

	// if the condition is true, then we're active.  The unit tests confirm it goes false.
	_, operatorStatus, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum()
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionTrue(operatorStatus.Conditions, "FeatureGatesUpgradeable"))
}
