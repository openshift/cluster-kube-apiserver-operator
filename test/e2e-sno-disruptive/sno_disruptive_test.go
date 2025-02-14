package e2e_sno_disruptive

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/staticpod/startupmonitor/annotations"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	commontesthelpers "github.com/openshift/library-go/test/library/encryption"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

func TestFallback(tt *testing.T) {
	t := commontesthelpers.NewE(tt)
	cs := getClients(t)
	ctx := context.TODO()

	t.Log("Starting the fallback test")
	clusterStateWaitPollTimeout, clusterMustBeReadyForBeforeTest, clusterMustBeReadyFor, waitForFallbackDegradedConditionTimeout := fallbackTimeoutsForCurrentPlatform(t, cs)

	// before starting a new test make sure the current state of the cluster is good
	ensureClusterInGoodState(ctx, t, cs, clusterStateWaitPollTimeout, clusterMustBeReadyForBeforeTest)

	// cause a disruption
	cfg := getDefaultUnsupportedConfigForCurrentPlatform(t, cs)
	cfg["apiServerArguments"] = map[string][]string{"non-existing-flag": {"true"}}
	setUnsupportedConfig(t, cs, cfg)

	// validate if the fallback condition is reported and the cluster is stable
	waitForFallbackDegradedCondition(ctx, t, cs, waitForFallbackDegradedConditionTimeout)
	nodeName, failedRevision := assertFallbackOnNodeStatus(t, cs)
	assertKasPodAnnotatedOnNode(t, cs, failedRevision, nodeName)

	// clean up and some extra time is needed to wait for the KAS operator to be ready
	setUnsupportedConfig(t, cs, getDefaultUnsupportedConfigForCurrentPlatform(t, cs))
	err := waitForClusterInGoodState(ctx, t, cs, clusterStateWaitPollTimeout, clusterMustBeReadyFor)
	require.NoError(t, err)
}

// ensureClusterInGoodState makes sure the cluster is not progressing for mustBeReadyFor period
// in addition in an HA env it applies getDefaultUnsupportedConfigForCurrentPlatform so that the feature is enabled before the tests starts
func ensureClusterInGoodState(ctx context.Context, t testing.TB, cs clientSet, waitPollTimeout, mustBeReadyFor time.Duration) {
	setUnsupportedConfig(t, cs, getDefaultUnsupportedConfigForCurrentPlatform(t, cs))
	err := waitForClusterInGoodState(ctx, t, cs, waitPollTimeout, mustBeReadyFor)
	require.NoError(t, err)
}

// waitForClusterInGoodState checks if the cluster is not progressing
func waitForClusterInGoodState(ctx context.Context, t testing.TB, cs clientSet, waitPollTimeout, mustBeReadyFor time.Duration) error {
	t.Helper()

	startTs := time.Now()
	t.Logf("Waiting %s for the cluster to be in a good condition, interval = 20s, timeout %v", mustBeReadyFor.String(), waitPollTimeout)

	return wait.Poll(20*time.Second, waitPollTimeout, func() (bool, error) {
		ckaso, err := cs.Operator.Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			t.Log(err)
			return false, nil /*retry*/
		}

		// Check if any node is still progressing
		for _, ns := range ckaso.Status.NodeStatuses {
			if ckaso.Status.LatestAvailableRevision != ns.CurrentRevision || ns.TargetRevision > 0 {
				t.Logf("Node %s is progressing, latestAvailableRevision: %v, currentRevision: %v, targetRevision: %v",
					ns.NodeName, ckaso.Status.LatestAvailableRevision, ns.CurrentRevision, ns.TargetRevision)
				return false, nil /*retry*/
			}
		}

		// Verify operator conditions
		ckasoAvailable := v1helpers.IsOperatorConditionTrue(ckaso.Status.Conditions, "StaticPodsAvailable")
		ckasoNotProgressing := v1helpers.IsOperatorConditionFalse(ckaso.Status.Conditions, "NodeInstallerProgressing")
		ckasoNotDegraded := v1helpers.IsOperatorConditionFalse(ckaso.Status.Conditions, "NodeControllerDegraded")

		// If cluster has been stable for the required time, return success
		if time.Since(startTs) > mustBeReadyFor && ckasoAvailable && ckasoNotProgressing && ckasoNotDegraded {
			t.Logf("The cluster has been in good condition for %s", mustBeReadyFor.String())
			return true, nil /*done*/
		}

		return false, nil /*wait a bit more*/
	})
}

// setUnsupportedConfig simply sets UnsupportedConfigOverrides config to the provided cfg
func setUnsupportedConfig(t testing.TB, cs clientSet, cfg map[string]interface{}) {
	t.Helper()

	t.Logf("Setting UnsupportedConfigOverrides to %v", cfg)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	err = retry.OnError(retry.DefaultRetry, func(error) bool { return true }, func() error {
		ckaso, err := cs.Operator.Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			t.Log(err)
			return err
		}
		ckaso.Spec.UnsupportedConfigOverrides.Raw = raw
		_, err = cs.Operator.Update(context.TODO(), ckaso, metav1.UpdateOptions{})
		if err != nil {
			t.Log(err)
		}
		return err
	})
	require.NoError(t, err)
}

// waitForFallbackDegradedCondition waits until StaticPodFallbackRevisionDegraded condition is set to true
func waitForFallbackDegradedCondition(ctx context.Context, t testing.TB, cs clientSet, waitPollTimeout time.Duration) {
	t.Helper()

	t.Logf("Waiting for StaticPodFallbackRevisionDegraded condition, interval = 20s, timeout = %v", waitPollTimeout)
	err := wait.Poll(20*time.Second, waitPollTimeout, func() (bool, error) {
		ckaso, err := cs.Operator.Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			t.Logf("unable to get kube-apiserver-operator resource: %v", err)
			return false, nil /*retry*/
		}

		if v1helpers.IsOperatorConditionTrue(ckaso.Status.Conditions, "StaticPodFallbackRevisionDegraded") {
			return true, nil /*done*/
		}

		t.Logf("StaticPodFallbackRevisionDegraded condition hasn't been set yet")
		return false, nil /*retry*/
	})
	require.NoError(t, err)
}

func assertFallbackOnNodeStatus(t testing.TB, cs clientSet) (string, int32) {
	t.Helper()

	t.Log("Checking if a NodeStatus has been updated to report the fallback condition")

	ckaso, err := cs.Operator.Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	var nodeName string
	var failedRevision int32
	for _, ns := range ckaso.Status.NodeStatuses {
		if ns.LastFallbackCount != 0 && len(nodeName) > 0 {
			t.Fatalf("multiple node statuses report the fallback, previously on node %v, revision %v, currently on node %v, revision %v", nodeName, failedRevision, ns.NodeName, ns.LastFailedRevision)
		}
		if ns.LastFallbackCount != 0 {
			nodeName = ns.NodeName
			failedRevision = ns.LastFailedRevision
		}
	}

	t.Logf("The fallback has been reported on node %v, failed revision is %v", nodeName, failedRevision)
	return nodeName, failedRevision
}

func assertKasPodAnnotatedOnNode(t testing.TB, cs clientSet, expectedFailedRevision int32, nodeName string) {
	t.Helper()
	t.Logf("Verifying if a kube-apiserver pod has been annotated with revision: %v on node: %v", expectedFailedRevision, nodeName)

	kasPods, err := cs.Kube.CoreV1().Pods("openshift-kube-apiserver").List(context.TODO(), metav1.ListOptions{LabelSelector: "apiserver=true"})
	require.NoError(t, err)

	var kasPod corev1.Pod
	filteredKasPods := filterByNodeName(kasPods.Items, nodeName)
	switch len(filteredKasPods) {
	case 0:
		t.Fatalf("expected to find the kube-apiserver static pod on node %s but haven't found any", nodeName)
	case 1:
		kasPod = filteredKasPods[0]
	default:
		// this should never happen for static pod as they are uniquely named for each node
		podsOnCurrentNode := []string{}
		for _, filteredKasPod := range filteredKasPods {
			podsOnCurrentNode = append(podsOnCurrentNode, filteredKasPod.Name)
		}
		t.Fatalf("multiple kube-apiserver static pods for node %s found: %v", nodeName, podsOnCurrentNode)
	}

	if fallbackFor, ok := kasPod.Annotations[annotations.FallbackForRevision]; ok {
		if len(fallbackFor) == 0 {
			t.Fatalf("empty fallback revision label: %v on %s pod", annotations.FallbackForRevision, kasPod.Name)
		}
		revision, err := strconv.Atoi(fallbackFor)
		if err != nil || revision < 0 {
			t.Fatalf("invalid fallback revision: %v on pod: %s", fallbackFor, kasPod.Name)
		}
		return
	}

	t.Fatalf("kube-apiserver %v on node %v hasn't been annotated with %v", kasPod.Name, nodeName, annotations.FallbackForRevision)
}

func filterByNodeName(kasPods []corev1.Pod, currentNodeName string) []corev1.Pod {
	filteredKasPods := []corev1.Pod{}

	for _, potentialKasPods := range kasPods {
		if potentialKasPods.Spec.NodeName == currentNodeName {
			filteredKasPods = append(filteredKasPods, potentialKasPods)
		}
	}

	return filteredKasPods
}

// getDefaultUnsupportedConfigForCurrentPlatform returns a predefined config specific to the current platform that can be extended by the tests
// it facilitates testing on an HA cluster by setting "startupMonitor:true" which enables the feature
func getDefaultUnsupportedConfigForCurrentPlatform(t testing.TB, cs clientSet) map[string]interface{} {
	t.Helper()

	infraConfiguration, err := cs.Infra.Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	if infraConfiguration.Status.ControlPlaneTopology != configv1.SingleReplicaTopologyMode {
		return map[string]interface{}{"startupMonitor": true}
	}

	return map[string]interface{}{}
}

// fallbackTimeoutsForCurrentPlatform provides various timeouts that are tailored for the current platform
// TODO: add timeouts for AWS and GCP
// TODO: we should be able to return only a single per-platform specific timeout and derive the rest e.g. oneNodeRolloutTimeout
func fallbackTimeoutsForCurrentPlatform(t testing.TB, cs clientSet) (time.Duration, time.Duration, time.Duration, time.Duration) {
	/*
	 default timeouts that apply when the test is run on an SNO cluster

	 clusterStateWaitPollInterval:            is the max time after the cluster is considered not ready
	                                          it should match waitForFallbackDegradedConditionTimeout
	                                          because we don't know when the previous test finished

	 clusterMustBeReadyForBeforeTest:         the time that make sure the current state of the cluster is good
	                                          before starting a new test

	 clusterMustBeReadyFor:                   the time the cluster must stay stable

	 waitForFallbackDegradedConditionTimeout: set to 10 min, it should be much lower
	                                          the static pod monitor needs 5 min to fallback to the previous revision
	                                          but we don't know yet how much time it takes to start a new api server
	                                          including the time the server needs to become ready and be noticed by a Load Balancer
	                                          longer duration allows as to collect logs and the must-gather
	*/
	return 10 * time.Minute, // clusterStateWaitPollInterval
		1 * time.Minute, // clusterMustBeReadyForBeforeTest
		5 * time.Minute, // clusterMustBeReadyFor
		18 * time.Minute // waitForFallbackDegradedConditionTimeout
}
