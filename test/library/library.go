package library

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

var (
	WaitPollInterval = time.Second
	WaitPollTimeout  = 10 * time.Minute
)

var (
	waitForAPIRevisionSuccessThreshold = 3
	waitForAPIRevisionSuccessInterval  = 1 * time.Minute

	waitForAPIRevisionPollInterval = 30 * time.Second
	waitForAPIRevisionTimeout      = 15 * time.Minute
)

// GenerateNameForTest generates a name of the form `prefix + test name + random string` that
// can be used as a resource name. Convert the result to lowercase to use as a dns label.
func GenerateNameForTest(t *testing.T, prefix string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	require.NoError(t, err)
	name := []byte(fmt.Sprintf("%s%s-%016x", prefix, t.Name(), n.Int64()))
	// make the name (almost) suitable for use as a dns label
	// only a-z, 0-9, and '-' allowed
	name = regexp.MustCompile("[^a-zA-Z0-9]+").ReplaceAll(name, []byte("-"))
	// collapse multiple `-`
	name = regexp.MustCompile("-+").ReplaceAll(name, []byte("-"))
	// ensure no `-` at beginning or end
	return strings.Trim(string(name), "-")
}

// WaitForAPIServerToStabilizeOnTheSameRevision waits until all API Servers are running at the same revision.
// The API Servers must stay on the same revision for at least waitForAPIRevisionSuccessThreshold * waitForAPIRevisionSuccessInterval.
// Mainly because of the difference between the propagation time of triggering a new release and the actual roll-out.
//
// Observations:
//  rolling out a new version is not instant you need to account for a propagation time (~1/2 minutes)
//  rolling out a new version can take ~10 minutes
func WaitForAPIServerToStabilizeOnTheSameRevision(t *testing.T, podClient corev1client.PodInterface) {
	if err := wait.Poll(waitForAPIRevisionPollInterval, waitForAPIRevisionTimeout, mustSucceedMultipleTimes(waitForAPIRevisionSuccessThreshold, waitForAPIRevisionSuccessInterval, func() (bool, error) {
		return areAPIServersOnTheSameRevision(t, podClient)
	})); err != nil {
		t.Fatal(err)
	}
}

// areAPIServersOnTheSameRevision tries to find the current revision that the API servers are running at.
// The number of instances is calculated based on the number of running pods in a namespace.
// This should be okay because this function is meant to be used by WaitForAPIServerToStabilizeOnTheSameRevision which will wait at least waitForAPIRevisionSuccessThreshold * waitForAPIRevisionSuccessInterval
// The number of pods should stabilize in that period of time.
func areAPIServersOnTheSameRevision(t *testing.T, podClient corev1client.PodInterface) (bool, error) {
	revisionLabel := "revision"

	// do a live list so we never get confused about what revision we are on
	apiServerPods, err := podClient.List(context.TODO(), metav1.ListOptions{LabelSelector: "apiserver=true"})
	if err != nil {
		// ignore the errors as we hope it will succeed next time
		t.Logf("failed to list pods, err = %v (this error will be ignored)", err)
		return false, nil
	}

	goodRevisions, failingRevisions, progressing, err := getRevisions(revisionLabel, apiServerPods.Items)
	if err != nil {
		return false, err
	}
	if progressing {
		return false, nil
	}

	if len(goodRevisions) != 1 {
		return false, nil // api servers have not converged onto a single revision
	}
	revision, _ := goodRevisions.PopAny()
	if failingRevisions.Has(revision) {
		return false, fmt.Errorf("api server revision %s has both running and failed pods", revision)
	}

	return true, nil
}

func getRevisions(revisionLabel string, pods []corev1.Pod) (sets.String, sets.String, bool, error) {
	goodRevisions := sets.NewString()
	badRevisions := sets.NewString()

	if len(pods) == 0 {
		return nil, nil, true, nil
	}
	for _, apiServerPod := range pods {
		switch phase := apiServerPod.Status.Phase; phase {
		case corev1.PodRunning:
			if !podReady(apiServerPod) {
				return nil, nil, true, nil // pods are not fully ready
			}
			goodRevisions.Insert(apiServerPod.Labels[revisionLabel])
		case corev1.PodPending:
			return nil, nil, true, nil // pods are not fully ready
		case corev1.PodUnknown:
			return nil, nil, false, fmt.Errorf("api server pod %s in unknown phase", apiServerPod.Name)
		case corev1.PodSucceeded, corev1.PodFailed:
			// handle failed pods carefully to make sure things are healthy
			// since the API server should never exit, a succeeded pod is considered as failed
			badRevisions.Insert(apiServerPod.Labels[revisionLabel])
		default:
			// error in case new unexpected phases get added
			return nil, nil, false, fmt.Errorf("api server pod %s has unexpected phase %v", apiServerPod.Name, phase)
		}
	}
	return goodRevisions, badRevisions, false, nil
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// mustSucceedMultipleTimes calls f multiple times sleeping in between the invocations, it only returns true if all invocations are successful.
func mustSucceedMultipleTimes(n int, sleep time.Duration, f func() (bool, error)) func() (bool, error) {
	return func() (bool, error) {
		for i := 0; i < n; i++ {
			ok, err := f()
			if err != nil || !ok {
				return ok, err
			}
			time.Sleep(sleep)
		}
		return true, nil
	}
}
