package library

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

// WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotFailing waits for ClusterOperator/kube-apiserver to report
// status as active, not progressing, and not failing.
func WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotFailing(t *testing.T, client configclient.ConfigV1Interface) {
	err := wait.Poll(waitPollInterval, waitPollTimeout, func() (bool, error) {
		clusterOperator, err := client.ClusterOperators().Get("kube-apiserver", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			t.Logf("ClusterOperator/kube-apiserver does not yet exist.")
			return false, nil
		}
		if err != nil {
			return false, err
		}
		conditions := clusterOperator.Status.Conditions
		available := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, configv1.ConditionTrue)
		notProgressing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, configv1.ConditionFalse)
		notFailing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorFailing, configv1.ConditionFalse)
		done := available && notProgressing && notFailing
		t.Logf("ClusterOperator/kube-apiserver: Available: %v  Progressing: %v  Failing: %v", available, !notProgressing, !notFailing)
		return done, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
