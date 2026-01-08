package e2e

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	tokenctl "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	testlibrary "github.com/openshift/library-go/test/library"
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"
	"github.com/openshift/library-go/test/ote"
)

const (
	interval       = 1 * time.Second
	regularTimeout = 30 * time.Second
)

// TestBoundTokenSignerController verifies the expected behavior of the controller
// with respect to the resources it manages.
//
// Note: The operator-secret-deletion sub-test will trigger TWO API server rollouts:
// 1. First rollout when new keypair is created (configmap has both old and new keys)
// 2. Second rollout during cleanup to reset configmap to single key
// Each rollout waits for completion before proceeding to avoid cluster instability.
//
// IMPORTANT: This test requires extended timeout as it triggers TWO rollouts and waits for cluster self-healing:
// 1. First rollout after operator secret deletion (~15-30 minutes)
// 2. Second rollout during cleanup to reset configmap (~15-60 minutes, can be slower)
// 3. Cluster self-healing after key rotation (~10-20 minutes for operators to get fresh tokens)
// Each rollout is verified in 3 steps: start progressing, pods stabilize, operator becomes healthy
// Run with: go test -v -timeout 120m ./test/e2e -run TestBoundTokenSignerController
//
// TODO: CNTRLPLANE-2223 - Migrate this test to OTE ginkgo framework
func TestBoundTokenSignerController(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Note: We do NOT wait for API server stabilization at the start because
	// the first two sub-tests (operand-secret-deletion and configmap-deletion)
	// verify controller behavior without triggering rollouts.
	// The operator-secret-deletion test waits for rollouts at strategic points:
	// after creating the new keypair and after cleanup.
	t.Logf("Starting test - controller behavior will be verified")

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// The operand secret should be recreated after deletion.
	t.Run("operand-secret-deletion", func(t *testing.T) {
		// Verify the operand secret exists before attempting deletion
		_, err := kubeClient.Secrets(targetNamespace).Get(context.TODO(), tokenctl.SigningKeySecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Logf("Secret %s does not exist initially, will verify it gets created", tokenctl.SigningKeySecretName)
		} else {
			require.NoError(t, err)
			// Delete the operand secret
			err = kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
			require.NoError(t, err)
			t.Logf("Deleted secret %s, waiting for recreation", tokenctl.SigningKeySecretName)
		}

		// Verify it gets recreated with expected data
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
		t.Logf("Secret %s successfully recreated with expected data", tokenctl.SigningKeySecretName)
	})

	// The operand config map should be recreated after deletion.
	// Note: it will roll out a new version
	t.Run("configmap-deletion", func(t *testing.T) {
		// Verify the configmap exists before attempting deletion
		_, err := kubeClient.ConfigMaps(targetNamespace).Get(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Logf("ConfigMap %s does not exist initially, will verify it gets created", tokenctl.PublicKeyConfigMapName)
		} else {
			require.NoError(t, err)
			// Delete the configmap
			err = kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
			require.NoError(t, err)
			t.Logf("Deleted configmap %s, waiting for recreation", tokenctl.PublicKeyConfigMapName)
		}

		// Verify it gets recreated with expected data
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
		})
		t.Logf("ConfigMap %s successfully recreated with expected data", tokenctl.PublicKeyConfigMapName)

		// Note: deletion triggers a roll-out, but we don't wait for it to complete here
		// to avoid test timeouts. The rollout will complete asynchronously.
	})

	// The secret in the operator namespace should be recreated with a new keypair
	// after deletion. The configmap in the operand namespace should be updated
	// immediately, and the secret once the configmap is present on all nodes.
	//
	// Note: it will roll out a new version
	t.Run("operator-secret-deletion", func(t *testing.T) {
		// Verify the original operator secret exists before deletion
		_, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Logf("Operator secret %s does not exist initially, skipping test", tokenctl.NextSigningKeySecretName)
			t.SkipNow()
		}
		require.NoError(t, err)

		// Cleanup: Reset configmap to single public key and wait for API server to stabilize.
		defer func() {
			t.Logf("Cleanup: Deleting configmap to reset to single public key")
			err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for API server pods to stabilize on same revision
			t.Logf("Cleanup: Waiting for API server pods to reach same revision")
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
			t.Logf("Cleanup: API server pods are on same revision")
		}()

		// Delete the operator secret
		t.Logf("Deleting operator secret %s", tokenctl.NextSigningKeySecretName)
		err = kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)

		// Wait for secret to be recreated with a new keypair
		t.Logf("Waiting for operator secret to be recreated with new keypair")
		var newOperatorSecret *corev1.Secret
		err = wait.PollImmediate(interval, regularTimeout, func() (done bool, err error) {
			newOperatorSecret, err = kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			if err != nil {
				t.Logf("failed to retrieve operator secret: %v", err)
				return false, nil
			}
			return true, nil
		})
		require.NoError(t, err)
		t.Logf("Operator secret recreated successfully")

		newOperatorPublicKey := newOperatorSecret.Data[tokenctl.PublicKeyKey]
		newOperatorPrivateKey := newOperatorSecret.Data[tokenctl.PrivateKeyKey]

		// Keypair should have changed
		require.NotEqual(t, operatorPublicKey, newOperatorPublicKey)
		require.NotEqual(t, operatorPrivateKey, newOperatorPrivateKey)
		t.Logf("Verified keypair has changed")

		// The certs configmap should include the previous and current public keys
		t.Logf("Verifying configmap contains both old and new public keys")
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
			"service-account-002.pub": string(newOperatorPublicKey),
		})
		t.Logf("ConfigMap verified with both public keys")

		// Wait for rollout to complete before checking operand secret
		// The operand secret is only updated after the new key is rolled out to all nodes

		// Step 1: Verify rollout starts (operator should become Progressing=True)
		t.Logf("Waiting for kube-apiserver operator to start progressing after operator secret deletion")
		err = ote.WaitForClusterOperatorStatus(context.TODO(), t, configClient.ClusterOperators(), "kube-apiserver",
			map[string]string{"Progressing": "True"},
			100*time.Second, 1.0)
		require.NoError(t, err, "kube-apiserver operator did not start progressing")
		t.Logf("kube-apiserver operator started progressing")

		// Step 2: Wait for pods to reach same revision
		t.Logf("Waiting for API server pods to reach same revision")
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
		t.Logf("API server pods are on same revision")

		// Step 3: Wait for cluster operator to become healthy
		// Using OTE helper from library-go PR #2050 to test those functions
		t.Logf("Waiting for kube-apiserver operator to become healthy (Available=True, Progressing=False, Degraded=False)")
		err = ote.WaitForClusterOperatorHealthy(context.TODO(), t, configClient.ClusterOperators(), "kube-apiserver", 25*time.Minute, 1.0)
		require.NoError(t, err, "kube-apiserver operator did not reach healthy state")

		// Check that the operand secret is updated with the new keypair
		t.Logf("Verifying operand secret is updated with new keypair")
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, newOperatorSecret.Data)
		t.Logf("Operand secret successfully updated with new keypair")
	})
}

// checkBoundTokenOperandSecret checks that the operand secret is
// populated with the expected data.
func checkBoundTokenOperandSecret(t *testing.T, kubeClient *clientcorev1.CoreV1Client, timeout time.Duration, expectedData map[string][]byte) {
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
func checkCertConfigMap(t *testing.T, kubeClient *clientcorev1.CoreV1Client, expectedData map[string]string) {
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

// TestTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
//
// This test calls the shared testTokenRequestAndReview function which
// can be called from both standard Go tests and Ginkgo tests.
//
// This situation is temporary until we test the new e2e-gcp-operator-serial-ote job.
// Eventually all tests will be run only as part of the OTE framework.
func TestTokenRequestAndReview(t *testing.T) {
	testTokenRequestAndReview(t)
}
