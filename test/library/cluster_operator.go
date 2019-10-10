package library

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

// WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded waits for ClusterOperator/kube-apiserver to report
// status as active, not progressing, and not failing.
func WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t *testing.T, client configv1client.ConfigV1Interface) {
	t.Helper()
	time.Sleep(time.Minute) // make sure we are not racing against an initial observation of change

	if err := wait.Poll(WaitPollInterval, WaitPollTimeout, func() (bool, error) {
		clusterOperator, err := client.ClusterOperators().Get("kube-apiserver", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			fmt.Println("ClusterOperator/kube-apiserver does not yet exist.")
			return false, nil
		}
		if err != nil {
			fmt.Println("Unable to retrieve ClusterOperator/kube-apiserver:", err)
			return false, nil
		}

		conditions := clusterOperator.Status.Conditions
		available := clusteroperatorhelpers.IsStatusConditionTrue(conditions, configv1.OperatorAvailable)
		notProgressing := clusteroperatorhelpers.IsStatusConditionFalse(conditions, configv1.OperatorProgressing)
		notDegraded := clusteroperatorhelpers.IsStatusConditionFalse(conditions, configv1.OperatorDegraded)
		done := available && notProgressing && notDegraded &&
			// make sure that we have not been progressing for a while so that multi-stage rollouts are correctly accounted for
			time.Since(clusteroperatorhelpers.FindStatusCondition(conditions, configv1.OperatorProgressing).LastTransitionTime.Time) > time.Minute
		fmt.Printf("ClusterOperator/kube-apiserver: Available: %v  Progressing: %v  Degraded: %v  Done: %v\n", available, !notProgressing, !notDegraded, done)

		return done, nil
	}); err != nil {
		t.Fatal(err)
	}
}
