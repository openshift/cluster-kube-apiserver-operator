package library

import (
	"testing"

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
func WaitForNextKubeAPIServerOperatorConfigGenerationToFinishProgressing(t *testing.T, client *operatorclient.OperatorV1Client, startingTime *metav1.Time) {
	// wait for Available=true and Progressing=false to stop
	err := wait.PollImmediate(WaitPollInterval, WaitPollTimeout, func() (bool, error) {
		config, err := client.KubeAPIServers().Get("cluster", metav1.GetOptions{})
		if err != nil {
			t.Log(err)
			return false, nil
		}

		if doneProgressing := operatorhelpers.IsOperatorConditionFalse(config.Status.Conditions, operatorv1.OperatorStatusTypeProgressing); !doneProgressing {
			t.Logf("KubeAPIServer/cluster: waiting for kube-apiserver is not done progressing")
			return false, nil
		}

		progressingCondition := operatorhelpers.FindOperatorCondition(config.Status.Conditions, operatorv1.OperatorStatusTypeProgressing)
		if progressingCondition.LastTransitionTime.Before(startingTime) {
			t.Logf("KubeAPIServer/cluster: has not yet started: %v", progressingCondition.Message)
			return false, nil
		}

		return true, nil
	})
	require.NoError(t, err)
}
