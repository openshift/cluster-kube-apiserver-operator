package e2e

import (
	"context"
	"reflect"
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

	g.It("[Operator][Serial] TestBoundTokenOperatorSecretDeletion [Timeout:90m][Late][Disruptive]", func() {
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
	t.Log("pre-condition: waiting for all ClusterOperators to be stable")
	err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
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

	t.Log("waiting for all ClusterOperators to recover after key rotation rollouts")
	err = waitForAllClusterOperatorsStableWithBounce(t, configClient, kubeClient, clusterStabilityTimeout)
	require.NoError(t, err)

	// Ensure the cert configmap is always removed at the end of the test so
	// it will contain only the current public key. This is essential to
	// allowing repeated invocations of the containing test.
	defer func() {
		t.Log("cleaning up: deleting public key configmap (triggers third rollout)")
		err := kubeClient.ConfigMaps(targetNamespace).Delete(ctx, tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)

		t.Log("waiting for kube-apiserver pods to stabilize after cleanup rollout")
		err = libgotest.WaitForPodsToStabilizeOnTheSameRevision(t, kubeClient.Pods(targetNamespace), "apiserver=true",
			rolloutSuccessThreshold, rolloutSuccessInterval, rolloutPollInterval, rolloutTimeout)
		require.NoError(t, err)

		t.Log("waiting for all ClusterOperators to stabilize after cleanup rollout")
		err = waitForAllClusterOperatorsStableWithBounce(t, configClient, kubeClient, clusterStabilityTimeout)
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

// bounceCrashLoopingPods deletes all pods in CrashLoopBackOff across
// openshift-* namespaces so their deployment/daemonset controllers recreate
// them with fresh projected SA tokens. After signing key rotation, every
// operator using a projected SA token will crash-loop with Unauthorized
// until it receives a token signed by the new key.
func bounceCrashLoopingPods(ctx context.Context, t testing.TB, kubeClient *clientcorev1.CoreV1Client) {
	namespaces, err := kubeClient.Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("failed to list namespaces: %v", err)
		return
	}
	bounced := 0
	for _, ns := range namespaces.Items {
		if !strings.HasPrefix(ns.Name, "openshift-") {
			continue
		}
		pods, err := kubeClient.Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Logf("failed to list pods in %s: %v", ns.Name, err)
			continue
		}
		for _, pod := range pods.Items {
			if isPodCrashLooping(pod) {
				t.Logf("deleting crash-looping pod %s/%s to refresh SA token", ns.Name, pod.Name)
				if err := kubeClient.Pods(ns.Name).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					t.Logf("failed to delete pod %s/%s: %v", ns.Name, pod.Name, err)
				}
				bounced++
			}
		}
	}
	t.Logf("bounced %d crash-looping pods across openshift-* namespaces", bounced)
}

// isPodCrashLooping returns true if any container is in CrashLoopBackOff or
// has terminated with an error after at least one restart. The second check
// catches pods between CrashLoopBackOff cycles when the container state is
// Terminated/Error rather than Waiting/CrashLoopBackOff.
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

// waitForAllClusterOperatorsStable waits until all ClusterOperators in the cluster
// report Available=True, Progressing=False, Degraded=False. This ensures the
// entire cluster is stable after disruptive operations like signing key rotation.
func waitForAllClusterOperatorsStable(t testing.TB, client configclient.ConfigV1Interface, timeout time.Duration) error {
	return waitForAllClusterOperatorsStableWithBounce(t, client, nil, timeout)
}

// waitForAllClusterOperatorsStableWithBounce waits until all ClusterOperators
// are stable. When kubeClient is non-nil, it also bounces crash-looping pods
// on each poll iteration to accelerate recovery from SA token invalidation.
func waitForAllClusterOperatorsStableWithBounce(t testing.TB, client configclient.ConfigV1Interface, kubeClient *clientcorev1.CoreV1Client, timeout time.Duration) error {
	ctx := context.TODO()
	return wait.PollImmediate(30*time.Second, timeout, func() (bool, error) {
		if kubeClient != nil {
			bounceCrashLoopingPods(ctx, t, kubeClient)
		}
		coList, err := client.ClusterOperators().List(ctx, metav1.ListOptions{})
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
