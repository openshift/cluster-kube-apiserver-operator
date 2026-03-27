package e2e

import (
	"context"
	"reflect"
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

	g.It("[Operator][Serial] TestBoundTokenOperatorSecretDeletion [Timeout:150m][Late][Disruptive]", func() {
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

// testBoundTokenOperatorSecretDeletion verifies the secret in the operator namespace
// is recreated with a new keypair after deletion. The configmap in the operand namespace
// should be updated immediately, and the secret once the configmap is present on all nodes.
//
// Note: it will roll out a new version
func testBoundTokenOperatorSecretDeletion(t testing.TB) {
	kubeConfig, err := libgotest.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Pre-condition: cluster must be stable before we introduce disruption.
	// Prior tests (e.g. cert rotation) may have left kube-apiserver mid-rollout
	// which would compound with our key rotation rollout.
	t.Log("pre-condition: waiting for all ClusterOperators to be stable")
	err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
	require.NoError(t, err)

	// Retrieve the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// Delete the operator secret
	err = kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
	require.NoError(t, err)

	// deletion triggers a roll-out - wait until a new version has been rolled out
	err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
	require.NoError(t, err)

	// Ensure that the cert configmap is always removed at the end of the test
	// to ensure it will contain only the current public key. This property is
	// essential to allowing repeated invocations of the containing test.
	defer func() {
		err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)

		// Use a higher success threshold (15) with a longer interval (3 min) to
		// ensure pods stay stable for 45 minutes (15 × 3 min). This accounts for
		// delayed rollouts triggered by configmap deletion, where the operator's
		// propagation delay can cause the stability check to falsely pass on the
		// old revision. KAS rollouts can take 15-20 minutes each.
		t.Log("waiting for all ClusterOperators to stabilize")
		err = waitForAllClusterOperatorsStable(t, configClient, clusterStabilityTimeout)
		require.NoError(t, err)
	}()

	// Wait for secret to be recreated with a new keypair
	var newOperatorSecret *corev1.Secret
	err = wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
		newOperatorSecret, err = kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
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

	// Keypair should have changed
	require.NotEqual(t, operatorPublicKey, newOperatorPublicKey)
	require.NotEqual(t, operatorPrivateKey, newOperatorPrivateKey)

	// The certs configmap should include the previous and current public keys
	checkCertConfigMap(t, kubeClient, map[string]string{
		"service-account-001.pub": string(operatorPublicKey),
		"service-account-002.pub": string(newOperatorPublicKey),
	})

	// Check that the operand secret is updated within the promotion timeout
	checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, newOperatorSecret.Data)
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

// waitForAllClusterOperatorsStable waits until all ClusterOperators in the cluster
// report Available=True, Progressing=False, Degraded=False. This ensures the
// entire cluster is stable after disruptive operations like signing key rotation.
func waitForAllClusterOperatorsStable(t testing.TB, client configclient.ConfigV1Interface, timeout time.Duration) error {
	return wait.PollImmediate(30*time.Second, timeout, func() (bool, error) {
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
