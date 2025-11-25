package e2e

import (
	"context"
	"reflect"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

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
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"
)

const (
	interval       = 1 * time.Second
	regularTimeout = 30 * time.Second
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestTokenRequestAndReview", func() {
		TestTokenRequestAndReview(g.GinkgoTB())
	})

	g.It("TestBoundTokenSignerController_OperandSecretDeletion", func() {
		TestBoundTokenSignerController_OperandSecretDeletion(g.GinkgoTB())
	})

	g.It("TestBoundTokenSignerController_ConfigMapDeletion", func() {
		TestBoundTokenSignerController_ConfigMapDeletion(g.GinkgoTB())
	})

	g.It("TestBoundTokenSignerController_OperatorSecretDeletion", func() {
		TestBoundTokenSignerController_OperatorSecretDeletion(g.GinkgoTB())
	})
})

// TestTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
func TestTokenRequestAndReview(t testing.TB) {
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

// TestBoundTokenSignerController_OperandSecretDeletion verifies that the operand
// secret is recreated after deletion.
func TestBoundTokenSignerController_OperandSecretDeletion(t testing.TB) {
	t.Skip("Skipped - originally skipped in bound_sa_token_test.go")

	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)

	// Delete the operand secret
	err = kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
	require.NoError(t, err)

	// Verify it's recreated with the same data
	checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
}

// TestBoundTokenSignerController_ConfigMapDeletion verifies that the operand
// config map is recreated after deletion and triggers a rollout.
func TestBoundTokenSignerController_ConfigMapDeletion(t testing.TB) {
	t.Skip("Skipped - originally skipped in bound_sa_token_test.go")

	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]

	// Delete the configmap
	err = kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
	require.NoError(t, err)

	// Verify it's recreated with the correct data
	checkCertConfigMap(t, kubeClient, map[string]string{
		"service-account-001.pub": string(operatorPublicKey),
	})

	// Wait for rollout to complete
	testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
}

// TestBoundTokenSignerController_OperatorSecretDeletion verifies that the operator
// secret is recreated with a new keypair after deletion, and that the configmap
// and operand secret are updated accordingly.
func TestBoundTokenSignerController_OperatorSecretDeletion(t testing.TB) {
	t.Skip("Skipped - originally skipped in bound_sa_token_test.go")

	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret before deletion
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// Delete the operator secret
	err = kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
	require.NoError(t, err)

	// Wait for rollout to complete
	testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

	// Ensure configmap is cleaned up at the end
	defer func() {
		err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
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
			t.Errorf("failed to retrieve template secret: %v", err)
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
			t.Errorf("failed to retrieve signing secret template: %v", err)
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
			t.Errorf("failed to retrieve cert configmap: %v", err)
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
