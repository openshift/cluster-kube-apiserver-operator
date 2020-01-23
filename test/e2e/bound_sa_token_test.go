package e2e

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	tokenctl "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	testlibrary "github.com/openshift/library-go/test/library"
)

const (
	interval       = 1 * time.Second
	regularTimeout = 30 * time.Second

	// Need a long time for promotion to account for the delay in
	// nodes being updated to a revision of the configmap that
	// contains the latest public key.
	promotionTimeout = 10 * time.Minute
)

// TestBoundTokenSignerController verifies the expected behavior of the controller
// with respect to the resources it manages.
func TestBoundTokenSignerController(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Wait for operator readiness
	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// The operand secret should be recreated after deletion.
	t.Run("operand-secret-deletion", func(t *testing.T) {
		err := kubeClient.Secrets(targetNamespace).Delete(tokenctl.SigningKeySecretName, &metav1.DeleteOptions{})
		require.NoError(t, err)
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
	})

	// The operand config map should be recreated after deletion.
	t.Run("configmap-deletion", func(t *testing.T) {
		err := kubeClient.ConfigMaps(targetNamespace).Delete(tokenctl.PublicKeyConfigMapName, &metav1.DeleteOptions{})
		require.NoError(t, err)
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
		})
	})

	// The secret in the operator namespace should be recreated with a new keypair
	// after deletion. The configmap in the operand namespace should be updated
	// immediately, and the secret once the configmap is present on all nodes.
	t.Run("operator-secret-deletion", func(t *testing.T) {
		// Delete the operator secret
		err := kubeClient.Secrets(operatorNamespace).Delete(tokenctl.NextSigningKeySecretName, &metav1.DeleteOptions{})
		require.NoError(t, err)

		// Ensure that the cert configmap is always removed at the end of the test
		// to ensure it will contain only the current public key. This property is
		// essential to allowing repeated invocations of the containing test.
		defer func() {
			err := kubeClient.ConfigMaps(targetNamespace).Delete(tokenctl.PublicKeyConfigMapName, &metav1.DeleteOptions{})
			require.NoError(t, err)
		}()

		// Wait for secret to be recreated with a new keypair
		var newOperatorSecret *corev1.Secret
		err = wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
			newOperatorSecret, err = kubeClient.Secrets(operatorNamespace).Get(tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
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
		checkBoundTokenOperandSecret(t, kubeClient, promotionTimeout, newOperatorSecret.Data)
	})
}

// checkBoundTokenOperandSecret checks that the operand secret is
// populated with the expected data.
func checkBoundTokenOperandSecret(t *testing.T, kubeClient *clientcorev1.CoreV1Client, timeout time.Duration, expectedData map[string][]byte) {
	err := wait.PollImmediate(interval, timeout, func() (done bool, err error) {
		secret, err := kubeClient.Secrets(operatorclient.TargetNamespace).Get(tokenctl.SigningKeySecretName, metav1.GetOptions{})
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
func checkCertConfigMap(t *testing.T, kubeClient *clientcorev1.CoreV1Client, expectedData map[string]string) {
	err := wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
		configMap, err := kubeClient.ConfigMaps(operatorclient.TargetNamespace).Get(tokenctl.PublicKeyConfigMapName, metav1.GetOptions{})
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

// TestTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
func TestTokenRequestAndReview(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	corev1client := kubeClient.CoreV1()

	// Create all test resources in a temp namespace that will be
	// removed at the end of the test to avoid requiring explicit
	// cleanup.
	ns, err := corev1client.Namespaces().Create(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-token-request-",
		},
	})
	require.NoError(t, err)
	defer func() {
		err := corev1client.Namespaces().Delete(ns.Name, nil)
		require.NoError(t, err)
	}()
	namespace := ns.Name

	sa, err := corev1client.ServiceAccounts(namespace).Create(&v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-service-account",
		},
	})
	require.NoError(t, err)

	treq, err := corev1client.ServiceAccounts(sa.Namespace).CreateToken(
		sa.Name,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				Audiences: []string{"auth.openshift.io"},
			},
		},
	)
	require.NoError(t, err)

	trev, err := kubeClient.AuthenticationV1().TokenReviews().Create(&authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: treq.Status.Token,
		},
	})
	require.NoError(t, err)
	require.Empty(t, trev.Status.Error)
	require.True(t, trev.Status.Authenticated)
}

// TestChangeServiceAccountIssuer checks that the operator considers
// the value set for Authentication.ServiceAccountIssuer when setting
// the configuration configmap for the apiserver pods.
func TestChangeServiceAccountIssuer(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	coreClient := kubeClient.CoreV1()
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	// Wait for operator readiness
	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)

	defaultIssuer := "auth.openshift.io"

	// Check that the default issuer is set in the operand config
	require.NoError(t, pollForOperandIssuer(t, coreClient, defaultIssuer))

	nonDefaultIssuer := "https://my-custom-issuer.com"
	// Update the issuer to a valid value (corner cases are unit tested)
	setServiceAccountIssuer(t, configClient, nonDefaultIssuer)
	// Check that the issuer has changed to the non-default value
	require.NoError(t, pollForOperandIssuer(t, coreClient, nonDefaultIssuer))

	// Clear the issuer
	setServiceAccountIssuer(t, configClient, "")
	// Check that the issuer has changed back to the default
	require.NoError(t, pollForOperandIssuer(t, coreClient, defaultIssuer))
}

func pollForOperandIssuer(t *testing.T, client clientcorev1.CoreV1Interface, expectedIssuer string) error {
	return wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
		configMap, err := client.ConfigMaps(operatorclient.TargetNamespace).Get("config", metav1.GetOptions{})
		if err != nil {
			t.Errorf("failed to retrieve apiserver config configmap: %v", err)
			return false, nil
		}
		// key has a .yaml extension but actual format is json
		rawConfig := configMap.Data["config.yaml"]
		if len(rawConfig) == 0 {
			t.Logf("config.yaml is empty in apiserver config configmap")
			return false, nil
		}
		config := map[string]interface{}{}
		if err := json.NewDecoder(bytes.NewBuffer([]byte(rawConfig))).Decode(&config); err != nil {
			t.Errorf("error parsing config, %v", err)
			return false, nil
		}
		issuers, found, err := unstructured.NestedStringSlice(config, "apiServerArguments", "service-account-issuer")
		if !found {
			t.Log("apiServerArguments.service-account-issuer not found in config")
			return false, nil
		}
		issuer := issuers[0]
		if !found || expectedIssuer != issuer {
			t.Logf("expected service account issuer to be %q, got %q", expectedIssuer, issuer)
			return false, nil
		}
		return true, nil
	})
}

func setServiceAccountIssuer(t *testing.T, client configclient.ConfigV1Interface, issuer string) {
	auth, err := client.Authentications().Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	auth.Spec.ServiceAccountIssuer = issuer
	_, err = client.Authentications().Update(auth)
	require.NoError(t, err)
}
