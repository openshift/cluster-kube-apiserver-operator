package e2e

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	securityv1client "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"

	"github.com/stretchr/testify/require"
)

func TestDefaultSCCUpgradeable(t *testing.T) {
	config, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	securityClient, err := securityv1client.NewForConfig(config)
	require.NoError(t, err, "failed to create client for security/v1")

	configClient, err := configclient.NewForConfig(config)
	require.NoError(t, err)

	operatorClient, err := operatorclient.NewForConfig(config)
	require.NoError(t, err)

	assertUpgradeableTrue := func() {
		test.WaitForKubeAPIServerOperatorStatus(t, operatorClient, func(cluster *operatorv1.KubeAPIServer) bool {
			condition := test.FindOperatorStatusCondition(cluster.Status.Conditions, "DefaultSecurityContextConstraintsUpgradeable")
			if condition != nil &&
				condition.Status == operatorv1.ConditionTrue &&
				condition.Reason == "AsExpected" {
				return true
			}

			return false
		})

		test.WaitForKubeAPIServerClusterOperatorStatus(t, configClient, func(co *configv1.ClusterOperator) bool {
			condition := test.FindClusterOperatorCondition(co, configv1.OperatorUpgradeable)
			if condition != nil && condition.Status == configv1.ConditionTrue {
				return true
			}

			return false
		})
	}

	assertUpgradeableFalse := func() {
		test.WaitForKubeAPIServerOperatorStatus(t, operatorClient, func(cluster *operatorv1.KubeAPIServer) bool {
			condition := test.FindOperatorStatusCondition(cluster.Status.Conditions, "DefaultSecurityContextConstraintsUpgradeable")
			if condition != nil &&
				condition.Status == operatorv1.ConditionFalse &&
				condition.Reason == "Mutated" {
				return true
			}

			return false
		})

		test.WaitForKubeAPIServerClusterOperatorStatus(t, configClient, func(co *configv1.ClusterOperator) bool {
			condition := test.FindClusterOperatorCondition(co, configv1.OperatorUpgradeable)
			if condition != nil &&
				condition.Status == configv1.ConditionFalse &&
				condition.Reason == "DefaultSecurityContextConstraints_Mutated" {
				return true
			}

			return false
		})
	}

	mutate, revert := prepare(t, "anyuid", securityClient)

	assertUpgradeableTrue()
	mutate()
	assertUpgradeableFalse()

	revert()
	assertUpgradeableTrue()
}

func prepare(t *testing.T, name string, client securityv1client.SecurityV1Interface) (mutate, revert func()) {
	mutate = func() {
		current, err := client.SecurityContextConstraints().Get(name, metav1.GetOptions{})
		require.NoError(t, err)

		current.Users = append(current.Users, "foo")

		_, err = client.SecurityContextConstraints().Update(current)
		require.NoError(t, err)
	}

	revert = func() {
		current, err := client.SecurityContextConstraints().Get(name, metav1.GetOptions{})
		require.NoError(t, err)

		users := make([]string, 0)
		for _, user := range current.Users {
			if user == "foo" {
				continue
			}

			users = append(users, user)
		}
		current.Users = users

		_, err = client.SecurityContextConstraints().Update(current)
		require.NoError(t, err)
	}

	return
}
