package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
)

func TestOperatorNamespace(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	_, err = kubeClient.CoreV1().Namespaces().Get(context.TODO(), "openshift-kube-apiserver-operator", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestOperandImageVersion(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operator, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
	require.NoError(t, err)
	for _, operandVersion := range operator.Status.Versions {
		if operandVersion.Name == "kube-apiserver" {
			require.Regexp(t, `^1\.\d*\.\d*`, operandVersion.Version)
			return
		}
	}
	require.Fail(t, "operator kube-apiserver image version not found")
}

func TestRevisionLimits(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, _, err := genericoperatorclient.NewStaticPodOperatorClient(
		clock.RealClock{},
		kubeConfig,
		operatorv1.GroupVersion.WithResource("kubeapiservers"),
		operatorv1.GroupVersion.WithKind("KubeAPIServer"),
		operator.ExtractStaticPodOperatorSpec,
		operator.ExtractStaticPodOperatorStatus)
	require.NoError(t, err)

	// Get current revision limits
	operatorSpec, _, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum(context.TODO())
	require.NoError(t, err)

	totalRevisionLimit := operatorSpec.SucceededRevisionLimit + operatorSpec.FailedRevisionLimit
	if operatorSpec.SucceededRevisionLimit == 0 {
		totalRevisionLimit += 5
	}
	if operatorSpec.FailedRevisionLimit == 0 {
		totalRevisionLimit += 5
	}

	revisions, err := getRevisionStatuses(kubeClient)
	require.NoError(t, err)

	// Check if revisions are being quickly created to test for operator hotlooping
	changes := 0
	pollsWithoutChanges := 0
	lastRevisionCount := len(revisions)
	err = wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		newRevisions, err := getRevisionStatuses(kubeClient)
		require.NoError(t, err)

		// If there are more revisions than the total allowed Failed and Succeeded revisions, then there must be some that
		// are InProgress or Unknown (since these do not count toward failed or succeeded), which could indicate zombie revisions.
		// Check total+1 to account for possibly a current new revision that just hasn't pruned off the oldest one yet.
		if len(newRevisions) > int(totalRevisionLimit)+1 {
			// TODO(marun) If number of revisions has been exceeded, need to give time for the pruning controller to
			// progress rather than immediately failing.
			// t.Errorf("more revisions (%v) than total allowed (%v): %+v", len(revisions), totalRevisionLimit, revisions)
		}

		// No revisions in the last 30 seconds probably means we're not rapidly creating new ones and can return
		if len(newRevisions)-lastRevisionCount == 0 {
			pollsWithoutChanges += 1
			if pollsWithoutChanges >= 30 {
				return true, nil
			}
		} else {
			pollsWithoutChanges = 0
		}
		changes += len(newRevisions) - lastRevisionCount
		if changes >= 20 {
			return true, fmt.Errorf("too many new revisions created, possible hotlooping detected")
		}
		lastRevisionCount = len(newRevisions)
		return false, nil
	})
	require.NoError(t, err)
}

func getRevisionStatuses(kubeClient *kubernetes.Clientset) (map[string]string, error) {
	configMaps, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return map[string]string{}, err
	}
	revisions := map[string]string{}
	for _, configMap := range configMaps.Items {
		if !strings.HasPrefix(configMap.Name, "revision-status-") {
			continue
		}
		if revision, ok := configMap.Data["revision"]; ok {
			revisions[revision] = configMap.Data["status"]
		}
	}
	return revisions, nil
}
