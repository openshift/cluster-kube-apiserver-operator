package e2e

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"

	"github.com/stretchr/testify/require"
)

const (
	conditionType = "DefaultSecurityContextConstraintsUpgradeable"
)

func TestRemoveStaleSCCUpgradeableCondition(t *testing.T) {
	config, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	client, err := operatorclient.NewForConfig(config)
	require.NoError(t, err)

	staleSCCUpgradeableCondition := &operatorv1.OperatorCondition{
		Type:               conditionType,
		Reason:             "Mutated",
		Status:             operatorv1.ConditionFalse,
		Message:            "e2e test added this stale condition",
		LastTransitionTime: metav1.Now(),
	}

	addStaleConditionWithRetry(t, client, staleSCCUpgradeableCondition)

	test.WaitForKubeAPIServerOperatorStatus(t, client, func(cluster *operatorv1.KubeAPIServer) bool {
		condition := test.FindOperatorStatusCondition(cluster.Status.Conditions, conditionType)
		if condition != nil {
			return false
		}

		return true
	})

}

func addStaleConditionWithRetry(t *testing.T, client operatorclient.OperatorV1Interface, condition *operatorv1.OperatorCondition) {
	var cluster *operatorv1.KubeAPIServer

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, err := client.KubeAPIServers().Get("cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}

		if test.FindOperatorStatusCondition(current.Status.Conditions, condition.Type) != nil {
			cluster = current
			return nil
		}

		desired := current.DeepCopy()
		desired.Status.Conditions = append(desired.Status.Conditions, *condition)

		if current, err = client.KubeAPIServers().UpdateStatus(desired); err != nil {
			return err
		}

		cluster = current
		return nil
	})

	require.NoErrorf(t, err, "[addStaleConditionWithRetry] failed to add stale condition type=%s - %v", condition.Type, err)
	require.NotNil(t, cluster)
}
