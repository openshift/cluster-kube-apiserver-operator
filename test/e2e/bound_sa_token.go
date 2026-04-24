package e2e

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	tokenctl "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	testlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	libgotest "github.com/openshift/library-go/test/library"
)

const (
	interval                = 1 * time.Second
	regularTimeout          = 30 * time.Second
	clusterStabilityTimeout = 60 * time.Minute

	// kubeletTokenRefreshGracePeriod is the time we wait after a KAS rollout
	// for kubelet to naturally refresh SA tokens on all nodes. Kubelet syncs
	// projected volumes every ~1 minute. Set to 5 minutes to give kubelet
	// sufficient time to refresh tokens while minimizing crash-loop duration
	// that can degrade operators.
	kubeletTokenRefreshGracePeriod = 5 * time.Minute
)

// newTestCoreV1Client creates a CoreV1 client for use in e2e tests.
func newTestCoreV1Client(t testing.TB) *clientcorev1.CoreV1Client {
	kubeConfig, err := libgotest.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	return kubeClient
}

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial] TestTokenRequestAndReview", func() {
		testTokenRequestAndReview(g.GinkgoTB())
	})

	g.It("[Operator][Serial] TestBoundTokenOperandSecretDeletion", func() {
		testBoundTokenOperandSecretDeletion(g.GinkgoTB())
	})

	g.It("[Operator][Serial] TestBoundTokenConfigMapDeletion", func() {
		testBoundTokenConfigMapDeletion(g.GinkgoTB())
	})

	g.It("[Operator][Serial] TestBoundTokenOperatorSecretDeletion [Timeout:120m][Late][Disruptive]", func() {
		testBoundTokenOperatorSecretDeletion(g.GinkgoTB())
	})
})

// testTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
func testTokenRequestAndReview(t testing.TB) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	corev1client := kubeClient.CoreV1()

	// Create all test resources in a temp namespace that will be
	// removed at the end of the test to avoid requiring explicit
	// cleanup.
	ns, err := corev1client.Namespaces().Create(context.TODO(), &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-token-request-",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		err := corev1client.Namespaces().Delete(context.TODO(), ns.Name, metav1.DeleteOptions{})
		require.NoError(t, err)
	}()
	namespace := ns.Name

	sa, err := corev1client.ServiceAccounts(namespace).Create(context.TODO(), &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-service-account",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	treq, err := corev1client.ServiceAccounts(sa.Namespace).CreateToken(context.TODO(),
		sa.Name,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				// Avoid specifying any audiences so that the token will be
				// issued for the default audience of the issuer.
			},
		},
		metav1.CreateOptions{})
	require.NoError(t, err)

	trev, err := kubeClient.AuthenticationV1().TokenReviews().Create(context.TODO(), &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: treq.Status.Token,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Empty(t, trev.Status.Error)
	require.True(t, trev.Status.Authenticated)
}

// testBoundTokenOperandSecretDeletion verifies the operand secret is recreated after deletion.
func testBoundTokenOperandSecretDeletion(t testing.TB) {
	kubeClient := newTestCoreV1Client(t)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)

	// The operand secret should be recreated after deletion.
	err = kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
	require.NoError(t, err)
	checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
}

// testBoundTokenConfigMapDeletion verifies the configmap is recreated after deletion.
// Note: it will roll out a new version
func testBoundTokenConfigMapDeletion(t testing.TB) {
	kubeClient := newTestCoreV1Client(t)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]

	// The operand config map should be recreated after deletion.
	err = kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
	require.NoError(t, err)
	checkCertConfigMap(t, kubeClient, map[string]string{
		"service-account-001.pub": string(operatorPublicKey),
	})
}

// testBoundTokenOperatorSecretDeletion verifies the secret in the operator
// namespace is recreated with a new keypair after deletion.
//
// This test triggers three KAS rollouts:
//  1. Deleting the operator secret causes signing key rotation -> rollout
//  2. The configmap is updated with old+new public keys -> rollout
//  3. Deleting the public key configmap (cleanup, via defer) -> rollout
//
// Rollouts 1 and 2 happen back-to-back and are caught by a single pod
// stability wait (the success-threshold counter resets when a new revision
// appears). Rollout 3 is triggered by the deferred cleanup which ensures
// the configmap is always removed even if assertions fail.
//
// After each set of rollouts, all crash-looping pods in openshift-*
// namespaces are bounced because signing key rotation invalidates every
// projected SA token cluster-wide, causing Unauthorized errors and
// CrashLoopBackOff that would otherwise take 30-60 min to self-heal.
func testBoundTokenOperatorSecretDeletion(t testing.TB) {
	ctx := context.TODO()

	kubeConfig, err := libgotest.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	const (
		rolloutSuccessThreshold = 6
		rolloutSuccessInterval  = 1 * time.Minute
		rolloutPollInterval     = 30 * time.Second
		rolloutTimeout          = 60 * time.Minute
	)

	// Pre-condition: cluster must be stable before we introduce disruption.
	// Prior tests (e.g. TestBoundTokenConfigMapDeletion) may have left
	// kube-apiserver mid-rollout which would compound with our key rotation.
	// Wait for extended stability (5 minutes continuous) to minimize compounding issues.
	t.Log("pre-condition: waiting for extended cluster stability (5 minutes continuous)")
	err = waitForExtendedClusterStability(t, configClient, 5*time.Minute, clusterStabilityTimeout)
	require.NoError(t, err)

	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(ctx, tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// Rollouts 1 & 2: deleting the operator secret triggers signing key
	// rotation (rollout 1) and the configmap update with old+new public
	// keys (rollout 2). The pod stability wait below catches both because
	// the success-threshold counter resets whenever a new revision appears.
	t.Log("deleting operator secret to trigger signing key rotation")
	err = kubeClient.Secrets(operatorNamespace).Delete(ctx, tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
	require.NoError(t, err)

	t.Log("waiting for kube-apiserver pods to stabilize after key rotation rollouts")
	err = libgotest.WaitForPodsToStabilizeOnTheSameRevision(t, kubeClient.Pods(targetNamespace), "apiserver=true",
		rolloutSuccessThreshold, rolloutSuccessInterval, rolloutPollInterval, rolloutTimeout)
	require.NoError(t, err)

	// Wait for SA token signing keys to propagate to all nodes BEFORE bouncing.
	// This is critical: if we bounce pods before kubelet has the new keys,
	// the recreated pods will still get invalid tokens and crash with Unauthorized.
	t.Log("waiting for SA token signing keys to propagate to all nodes")
	err = waitForSATokenKeysOnAllNodes(ctx, t, kubeClient, targetNamespace, tokenctl.PublicKeyConfigMapName, 15*time.Minute)
	require.NoError(t, err)

	// Give kubelet additional time to refresh projected tokens in existing pods
	t.Logf("waiting %v for kubelet to refresh projected SA tokens in running pods", kubeletTokenRefreshGracePeriod)
	time.Sleep(kubeletTokenRefreshGracePeriod)

	t.Log("bouncing remaining crash-looping pods to get fresh SA tokens")
	// Reduced from 25m to 20m to ensure we stay below monitor test thresholds.
	// With maxBouncesPerPod=25 and minTimeBetweenBounces=2m, max bounce time
	// is 50m, but most pods should recover much faster.
	err = bounceCrashLoopingPodsWithRetry(ctx, t, kubeClient, 20*time.Minute)
	require.NoError(t, err)

	t.Log("waiting for all ClusterOperators to recover after key rotation rollouts")
	err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
	require.NoError(t, err)

	// Ensure the configmap is always cleaned up even if assertions below
	// fail. This prevents leaving two public keys that would break
	// subsequent invocations of this test.
	defer func() {
		t.Log("cleaning up: deleting public key configmap (triggers third rollout)")
		err := kubeClient.ConfigMaps(targetNamespace).Delete(ctx, tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)

		t.Log("waiting for kube-apiserver pods to stabilize after cleanup rollout")
		err = libgotest.WaitForPodsToStabilizeOnTheSameRevision(t, kubeClient.Pods(targetNamespace), "apiserver=true",
			rolloutSuccessThreshold, rolloutSuccessInterval, rolloutPollInterval, rolloutTimeout)
		require.NoError(t, err)

		// Wait for SA token signing keys to propagate to all nodes
		t.Log("waiting for SA token signing keys to propagate to all nodes after cleanup")
		err = waitForSATokenKeysOnAllNodes(ctx, t, kubeClient, targetNamespace, tokenctl.PublicKeyConfigMapName, 15*time.Minute)
		require.NoError(t, err)

		// Give kubelet additional time to refresh projected tokens
		t.Logf("waiting %v for kubelet to refresh projected SA tokens after cleanup", kubeletTokenRefreshGracePeriod)
		time.Sleep(kubeletTokenRefreshGracePeriod)

		t.Log("bouncing remaining crash-looping pods to get fresh SA tokens")
		// Reduced from 25m to 20m to ensure we stay below monitor test thresholds.
		// With maxBouncesPerPod=25 and minTimeBetweenBounces=2m, max bounce time
		// is 30m, but most pods should recover much faster.
		err = bounceCrashLoopingPodsWithRetry(ctx, t, kubeClient, 20*time.Minute)
		require.NoError(t, err)

		t.Log("waiting for all ClusterOperators to stabilize after cleanup rollout")
		err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
		require.NoError(t, err)
	}()

	var newOperatorSecret *corev1.Secret
	err = wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
		newOperatorSecret, err = kubeClient.Secrets(operatorNamespace).Get(ctx, tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			t.Logf("failed to retrieve template secret: %v", err)
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)

	newOperatorPublicKey := newOperatorSecret.Data[tokenctl.PublicKeyKey]
	newOperatorPrivateKey := newOperatorSecret.Data[tokenctl.PrivateKeyKey]

	require.NotEqual(t, operatorPublicKey, newOperatorPublicKey)
	require.NotEqual(t, operatorPrivateKey, newOperatorPrivateKey)

	checkCertConfigMap(t, kubeClient, map[string]string{
		"service-account-001.pub": string(operatorPublicKey),
		"service-account-002.pub": string(newOperatorPublicKey),
	})

	const operandSecretTimeout = 5 * time.Minute
	checkBoundTokenOperandSecret(t, kubeClient, operandSecretTimeout, newOperatorSecret.Data)
}

// checkBoundTokenOperandSecret checks that the operand secret is
// populated with the expected data.
func checkBoundTokenOperandSecret(t testing.TB, kubeClient *clientcorev1.CoreV1Client, timeout time.Duration, expectedData map[string][]byte) {
	err := wait.PollImmediate(interval, timeout, func() (done bool, err error) {
		secret, err := kubeClient.Secrets(operatorclient.TargetNamespace).Get(context.TODO(), tokenctl.SigningKeySecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			t.Logf("failed to retrieve signing secret template: %v", err)
			return false, nil
		}
		if !reflect.DeepEqual(secret.Data, expectedData) {
			t.Log("secret data is not as expected")
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)
}

// checkCertConfigMap checks that the cert configmap is present and populated with
// the expected data.
func checkCertConfigMap(t testing.TB, kubeClient *clientcorev1.CoreV1Client, expectedData map[string]string) {
	err := wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
		configMap, err := kubeClient.ConfigMaps(operatorclient.TargetNamespace).Get(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			t.Logf("failed to retrieve cert configmap: %v", err)
			return false, nil
		}
		if !reflect.DeepEqual(configMap.Data, expectedData) {
			t.Log("secret data is not yet as expected")
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)
}

// bounceCrashLoopingPodsWithRetry continuously bounces crash-looping pods
// until all pods in openshift-* namespaces are healthy or timeout is reached.
// Uses smart strategies to minimize disruption:
// - Bounces in waves by namespace to reduce cluster load
// - Tracks recently bounced pods to avoid re-bouncing too quickly
// - Prioritizes operator pods over infrastructure pods
// - Limits max bounces per pod to prevent excessive restarts in monitor tests
func bounceCrashLoopingPodsWithRetry(ctx context.Context, t testing.TB, kubeClient *clientcorev1.CoreV1Client, timeout time.Duration) error {
	const pollInterval = 45 * time.Second           // Increased from 30s to give pods more time to recover
	const minTimeBetweenBounces = 120 * time.Second // Don't re-bounce the same pod within 2 minutes
	const maxBouncesPerPod = 25                     // Maximum bounces per pod to avoid monitor test failures (threshold is 20)

	var lastUnhealthyCount int
	totalBounced := 0
	bouncedPods := make(map[string]time.Time) // Track when each pod was last bounced
	bounceCount := make(map[string]int)       // Track how many times each pod was bounced

	t.Logf("bouncing crash-looping pods until healthy (timeout: %v)", timeout)
	err := wait.PollImmediate(pollInterval, timeout, func() (bool, error) {
		namespaces, err := kubeClient.Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Logf("failed to list namespaces: %v", err)
			return false, nil
		}

		unhealthyPods := 0
		bouncedThisRound := 0

		// Process namespaces in priority order: operators first, then infrastructure
		priorityNamespaces := []string{
			"openshift-kube-apiserver-operator",
			"openshift-authentication-operator",
			"openshift-kube-controller-manager-operator",
			"openshift-kube-scheduler-operator",
			"openshift-etcd-operator",
			"openshift-machine-api",                // Machine API controllers are critical for cluster operations
			"openshift-catalogd",                   // OLMv1 catalog daemon
			"openshift-operator-controller",        // OLMv1 operator controller
			"openshift-marketplace",                // OLM marketplace operator
			"openshift-operator-lifecycle-manager", // OLM lifecycle manager
		}

		// First pass: bounce high-priority operator namespaces
		for _, nsName := range priorityNamespaces {
			bounced, unhealthy, err := bouncePodsInNamespace(ctx, t, kubeClient, nsName, bouncedPods, bounceCount, minTimeBetweenBounces, maxBouncesPerPod)
			if err != nil {
				// Skip this namespace but continue - transient API errors shouldn't fail the whole operation
				continue
			}
			bouncedThisRound += bounced
			unhealthyPods += unhealthy
		}

		// Second pass: bounce remaining openshift-* namespaces
		for _, ns := range namespaces.Items {
			if !strings.HasPrefix(ns.Name, "openshift-") {
				continue
			}
			// Skip if already processed in priority pass
			isPriority := false
			for _, pns := range priorityNamespaces {
				if ns.Name == pns {
					isPriority = true
					break
				}
			}
			if isPriority {
				continue
			}

			bounced, unhealthy, err := bouncePodsInNamespace(ctx, t, kubeClient, ns.Name, bouncedPods, bounceCount, minTimeBetweenBounces, maxBouncesPerPod)
			if err != nil {
				// Skip this namespace but continue - transient API errors shouldn't fail the whole operation
				continue
			}
			bouncedThisRound += bounced
			unhealthyPods += unhealthy
		}

		totalBounced += bouncedThisRound

		if unhealthyPods > 0 {
			t.Logf("bounced %d pods this round, %d total; %d unhealthy pods remaining",
				bouncedThisRound, totalBounced, unhealthyPods)
			lastUnhealthyCount = unhealthyPods
			return false, nil
		}

		t.Logf("all pods healthy after bouncing %d pods total", totalBounced)
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("timed out after %v with %d unhealthy pods remaining (bounced %d pods total)",
			timeout, lastUnhealthyCount, totalBounced)
	}
	return nil
}

// bouncePodsInNamespace bounces crash-looping pods in a single namespace.
// Returns (number bounced, number unhealthy, error).
// If an error is returned, the counts are unreliable and should not be used.
func bouncePodsInNamespace(ctx context.Context, t testing.TB, kubeClient *clientcorev1.CoreV1Client, namespace string, bouncedPods map[string]time.Time, bounceCount map[string]int, minTimeBetweenBounces time.Duration, maxBouncesPerPod int) (int, int, error) {
	pods, err := kubeClient.Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("failed to list pods in %s: %v", namespace, err)
		return 0, 0, err
	}

	bounced := 0
	unhealthy := 0

	// Sort pods by restart count (highest first) to prioritize pods
	// closest to the monitor test failure threshold (20 restarts)
	sortPodsByRestartCount(pods.Items)

	for _, pod := range pods.Items {
		if !isPodCrashLooping(pod) {
			continue
		}

		unhealthy++

		if !isPodEligibleForBounce(pod) {
			continue
		}

		// Check if we've exceeded max bounces for this pod
		podKey := namespace + "/" + pod.Name
		if count := bounceCount[podKey]; count >= maxBouncesPerPod {
			t.Logf("skipping pod %s/%s: already bounced %d times (max: %d)",
				namespace, pod.Name, count, maxBouncesPerPod)
			continue
		}

		// Check if we bounced this pod recently
		if lastBounceTime, exists := bouncedPods[podKey]; exists {
			if time.Since(lastBounceTime) < minTimeBetweenBounces {
				continue // Don't re-bounce too quickly
			}
		}

		t.Logf("bouncing crash-looping pod %s/%s (restarts: %d, bounce count: %d/%d)",
			namespace, pod.Name, getPodRestartCount(pod), bounceCount[podKey], maxBouncesPerPod)

		if err := kubeClient.Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("failed to delete pod %s/%s: %v", namespace, pod.Name, err)
		} else {
			bouncedPods[podKey] = time.Now()
			bounceCount[podKey]++
			bounced++
		}
	}

	return bounced, unhealthy, nil
}

// isPodCrashLooping returns true if any container is in CrashLoopBackOff
// or has terminated with a non-zero exit code after at least one restart.
// The second check catches pods between CrashLoopBackOff cycles when the
// container state is Terminated/Error rather than Waiting/CrashLoopBackOff.
// Neither condition matches healthy Running pods.
func isPodCrashLooping(pod corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 && cs.RestartCount > 0 {
			return true
		}
	}
	return false
}

// isPodEligibleForBounce checks if a crash-looping pod should be bounced.
// Returns false for:
// - Static pods (managed by kubelet, not controllers)
// - Very recently created pods (need time to initialize)
// - Pods with extremely high restart counts (likely a different issue)
// - Infrastructure pods that shouldn't be bounced (guard pods, etc.)
func isPodEligibleForBounce(pod corev1.Pod) bool {
	// Don't bounce static pods (managed by kubelet, not controllers)
	if pod.Annotations != nil {
		if source, ok := pod.Annotations["kubernetes.io/config.source"]; ok && source == "file" {
			return false
		}
	}

	// Don't bounce very recently created pods (give them at least 45s to start)
	if time.Since(pod.CreationTimestamp.Time) < 45*time.Second {
		return false
	}

	// Don't bounce guard pods - they're intentionally single-shot
	// Use suffix check to avoid false positives like "safeguard-controller"
	if strings.HasSuffix(pod.Name, "-guard") {
		return false
	}

	// Don't bounce installer/pruner pods - they're jobs, not long-running
	// Use prefix check for more precise matching
	if strings.HasPrefix(pod.Name, "installer-") || strings.HasPrefix(pod.Name, "revision-pruner-") {
		return false
	}

	// Don't bounce pods with extremely high restart counts (>40)
	// These likely have a different issue than SA token expiry.
	// We use 40 (2x the monitor threshold of 20) to ensure we can still
	// bounce pods that are approaching the monitor failure threshold.
	restartCount := getPodRestartCount(pod)
	if restartCount > 40 {
		return false
	}

	return true
}

// getPodRestartCount returns the total restart count across all containers in the pod.
func getPodRestartCount(pod corev1.Pod) int {
	total := 0
	for _, cs := range pod.Status.ContainerStatuses {
		total += int(cs.RestartCount)
	}
	return total
}

// sortPodsByRestartCount sorts pods by total restart count in descending order
// (highest restart count first). This prioritizes bouncing pods closest to
// the monitor test failure threshold.
func sortPodsByRestartCount(pods []corev1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		return getPodRestartCount(pods[i]) > getPodRestartCount(pods[j])
	})
}

// waitForSATokenKeysOnAllNodes waits for the SA token signing key configmap
// to exist and be stable. This configmap is used by kubelet to issue SA tokens
// for application pods (it's not mounted in kube-apiserver itself).
//
// Strategy:
//  1. Verify the configmap exists and has the expected public keys
//  2. Verify it has been stable (not updated) for at least 30 seconds
//     (proves the operator has finished updating it)
//  3. This gives us confidence kubelet can start syncing the new keys
func waitForSATokenKeysOnAllNodes(ctx context.Context, t testing.TB, kubeClient *clientcorev1.CoreV1Client, namespace, configMapName string, timeout time.Duration) error {
	const pollInterval = 10 * time.Second
	const requiredStabilityDuration = 30 * time.Second

	t.Logf("waiting for configmap %s/%s to exist and stabilize", namespace, configMapName)

	var lastResourceVersion string
	var stableStart time.Time

	return wait.PollImmediate(pollInterval, timeout, func() (bool, error) {
		cm, err := kubeClient.ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Logf("configmap %s/%s not found yet", namespace, configMapName)
			lastResourceVersion = ""
			stableStart = time.Time{}
			return false, nil
		}
		if err != nil {
			t.Logf("failed to get configmap %s/%s: %v", namespace, configMapName, err)
			return false, nil
		}

		// Verify configmap has data
		if len(cm.Data) == 0 {
			t.Logf("configmap %s has no data keys yet", configMapName)
			lastResourceVersion = ""
			stableStart = time.Time{}
			return false, nil
		}

		// Check if resourceVersion changed (configmap was updated)
		currentResourceVersion := cm.ResourceVersion
		if lastResourceVersion != currentResourceVersion {
			t.Logf("configmap %s has %d keys, resourceVersion: %s", configMapName, len(cm.Data), currentResourceVersion)
			lastResourceVersion = currentResourceVersion
			stableStart = time.Now()
			return false, nil
		}

		// ConfigMap hasn't changed - check stability duration
		if stableStart.IsZero() {
			stableStart = time.Now()
			return false, nil
		}

		stableDuration := time.Since(stableStart)
		if stableDuration < requiredStabilityDuration {
			t.Logf("configmap %s stable for %v / %v required", configMapName, stableDuration.Round(time.Second), requiredStabilityDuration)
			return false, nil
		}

		t.Logf("configmap %s has been stable for %v with %d keys", configMapName, stableDuration.Round(time.Second), len(cm.Data))
		return true, nil
	})
}

// waitForAllClusterOperatorsStable waits until all ClusterOperators in the cluster
// report Available=True, Progressing=False, Degraded=False. This ensures the
// entire cluster is stable after disruptive operations like signing key rotation.
func waitForAllClusterOperatorsStable(t testing.TB, client configclient.ConfigV1Interface, timeout time.Duration) error {
	return wait.PollImmediate(60*time.Second, timeout, func() (bool, error) {
		coList, err := client.ClusterOperators().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			t.Logf("unable to list ClusterOperators: %v", err)
			return false, nil
		}
		allStable := true
		for _, co := range coList.Items {
			conditions := co.Status.Conditions
			available := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, configv1.ConditionTrue)
			notProgressing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, configv1.ConditionFalse)
			notDegraded := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, configv1.ConditionFalse)
			if !available || !notProgressing || !notDegraded {
				t.Logf("ClusterOperator/%s not stable: Available=%v Progressing=%v Degraded=%v", co.Name, available, !notProgressing, !notDegraded)
				allStable = false
			}
		}
		if allStable {
			t.Logf("all %d ClusterOperators are stable", len(coList.Items))
		}
		return allStable, nil
	})
}

// waitForExtendedClusterStability waits for the cluster to be continuously stable
// for a specified duration. This is stronger than waitForAllClusterOperatorsStable
// which only checks once - this ensures sustained stability.
func waitForExtendedClusterStability(t testing.TB, client configclient.ConfigV1Interface, stabilityDuration, timeout time.Duration) error {
	stableStart := time.Time{}

	return wait.PollImmediate(30*time.Second, timeout, func() (bool, error) {
		coList, err := client.ClusterOperators().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			t.Logf("unable to list ClusterOperators: %v", err)
			stableStart = time.Time{} // Reset stability timer
			return false, nil
		}

		allStable := true
		for _, co := range coList.Items {
			conditions := co.Status.Conditions
			available := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, configv1.ConditionTrue)
			notProgressing := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, configv1.ConditionFalse)
			notDegraded := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, configv1.ConditionFalse)
			if !available || !notProgressing || !notDegraded {
				allStable = false
				break
			}
		}

		if !allStable {
			// Reset the stability timer
			stableStart = time.Time{}
			return false, nil
		}

		// Cluster is stable this iteration
		if stableStart.IsZero() {
			// First time seeing stability, start the timer
			stableStart = time.Now()
			t.Logf("cluster became stable, waiting %v for continuous stability", stabilityDuration)
			return false, nil
		}

		// Check if we've been stable long enough
		stableDuration := time.Since(stableStart)
		if stableDuration >= stabilityDuration {
			t.Logf("cluster has been continuously stable for %v", stableDuration)
			return true, nil
		}

		t.Logf("cluster stable for %v / %v required", stableDuration.Round(time.Second), stabilityDuration)
		return false, nil
	})
}
