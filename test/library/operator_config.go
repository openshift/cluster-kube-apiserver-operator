package library

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

// GetKubeAPIServerOperatorConfigGeneration returns the current generation of the KubeApiServer/cluster resource.
func GetKubeAPIServerOperatorConfigGeneration(t *testing.T, operatorClient *operatorclient.OperatorV1Client) int64 {
	config, err := operatorClient.KubeAPIServers().Get("cluster", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return config.Generation
}

// WaitForNextKubeAPIServerOperatorConfigGenerationToFinishProgressing waits for a new KubeApiServer/cluster generation
// to start and finish progressing.
func WaitForNextKubeAPIServerOperatorConfigGenerationToFinishProgressing(t *testing.T, client *operatorclient.OperatorV1Client, generation int64) {

	// the generation gets updated before the status does, so note the timestamp on the old Progressing status
	var lastTransitionTime metav1.Time

	// wait for Available=true and Progressing=false to stop
	err := wait.Poll(WaitPollInterval, WaitPollTimeout, func() (bool, error) {
		config, err := client.KubeAPIServers().Get("cluster", metav1.GetOptions{})
		if err != nil {
			t.Log(err)
			return false, nil
		}
		if config.Generation == generation {
			t.Logf("KubeAPIServer/cluster: waiting for new generation > %d", generation)
			lastTransitionTime = operatorhelpers.FindOperatorCondition(config.Status.Conditions, operatorv1.OperatorStatusTypeProgressing).LastTransitionTime
			return false, nil
		}
		conditions := config.Status.Conditions
		progressingCondition := operatorhelpers.FindOperatorCondition(conditions, operatorv1.OperatorStatusTypeProgressing)
		progressing := operatorhelpers.IsOperatorConditionPresentAndEqual(conditions, operatorv1.OperatorStatusTypeProgressing, operatorv1.ConditionTrue)
		if !progressing && !progressingCondition.LastTransitionTime.After(lastTransitionTime.Time) {
			t.Logf("KubeAPIServer/cluster: waiting for kube-apiserver to start progressing (LastTransitionTime=%v)", progressingCondition.LastTransitionTime.UTC().Format(time.RFC3339))
			return false, nil
		}
		if progressing {
			t.Logf("KubeAPIServer/cluster: waiting for kube-apiserver to finish progressing: %v", progressingCondition.Message)
			return false, nil
		}
		available := operatorhelpers.IsOperatorConditionPresentAndEqual(conditions, operatorv1.OperatorStatusTypeAvailable, operatorv1.ConditionTrue)
		if !available {
			t.Logf("KubeAPIServer/cluster: waiting for kube-apiserver become available")
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)
}
