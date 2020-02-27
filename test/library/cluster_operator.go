package library

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

// WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded waits for ClusterOperator/kube-apiserver to report
// status as active, not progressing, and not failing.
func WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t *testing.T, client configclient.ConfigV1Interface) {
	err := wait.Poll(WaitPollInterval, WaitPollTimeout, func() (bool, error) {
		clusterOperator, err := client.ClusterOperators().Get("kube-apiserver", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			fmt.Println("ClusterOperator/kube-apiserver does not yet exist.")
			return false, nil
		}
		if err != nil {
			fmt.Println("Unable to retrieve ClusterOperator/kube-apiserver:", err)
			return false, err
		}
		conditions := clusterOperator.Status.Conditions
		available := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, configv1.ConditionTrue)
		notProgressing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, configv1.ConditionFalse)
		notDegraded := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, configv1.ConditionFalse)
		done := available && notProgressing && notDegraded
		fmt.Printf("ClusterOperator/kube-apiserver: Available: %v  Progressing: %v  Degraded: %v\n", available, !notProgressing, !notDegraded)
		return done, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type ClusterOperatorConditionFunc func(co *configv1.ClusterOperator) bool

func WaitForKubeAPIServerClusterOperatorStatus(t *testing.T, client configclient.ConfigV1Interface, f ClusterOperatorConditionFunc) (current *configv1.ClusterOperator) {
	return WaitForClusterOperatorStatus(t, "kube-apiserver", client, f)
}

func WaitForClusterOperatorStatus(t *testing.T, name string, client configclient.ConfigV1Interface, f ClusterOperatorConditionFunc) (current *configv1.ClusterOperator) {
	err := wait.Poll(WaitPollInterval, WaitPollTimeout, func() (done bool, pollErr error) {
		current, pollErr = client.ClusterOperators().Get(name, metav1.GetOptions{})
		if pollErr != nil {
			return
		}

		if current == nil || !f(current) {
			return
		}

		done = true
		return
	})

	require.NoErrorf(t, err, "[WaitForClusterOperatorStatus] wait.Poll returned error - %v", err)
	require.NotNil(t, current)
	return
}

func FindClusterOperatorCondition(co *configv1.ClusterOperator, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range co.Status.Conditions {
		if co.Status.Conditions[i].Type == conditionType {
			return &co.Status.Conditions[i]
		}
	}

	return nil
}
