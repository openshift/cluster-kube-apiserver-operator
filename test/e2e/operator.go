package e2e

import (
	"testing"

	"context"
	"fmt"
	g "github.com/onsi/ginkgo/v2"
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
	"strings"
	"time"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestOperatorNamespace", func() {
		TestOperatorNamespace(g.GinkgoTB())
	})

	g.It("TestOperandImageVersion", func() {
		TestOperandImageVersion(g.GinkgoTB())
	})

	g.It("TestRevisionLimits", func() {
		TestRevisionLimits(g.GinkgoTB())
	})

})

func TestOperatorNamespace(t testing.TB) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	_, err = kubeClient.CoreV1().Namespaces().Get(context.TODO(), "openshift-kube-apiserver-operator", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestOperandImageVersion(t testing.TB) {
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

func TestRevisionLimits(t testing.TB) {
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
	changes := 0
	pollsWithoutChanges := 0
	lastRevisionCount := len(revisions)
	err = wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		newRevisions, err := getRevisionStatuses(kubeClient)
		require.NoError(t, err)

		if len(newRevisions) > int(totalRevisionLimit)+1 {

		}

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
