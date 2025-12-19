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

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	tokenctl "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	testlibrary "github.com/openshift/library-go/test/library"
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"
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
// IMPORTANT: This test requires extended timeout as rollouts can take 15-20 minutes each.
// Run with: go test -v -timeout 40m ./test/e2e -run TestBoundTokenSignerController
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

		// Cleanup: Reset configmap to single public key and wait for cluster to stabilize.
		// This ensures the cluster is in a stable state for subsequent tests.
		defer func() {
			t.Logf("Cleanup: Deleting configmap to reset to single public key")
			err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for cleanup rollout to complete (pods on same revision)
			t.Logf("Cleanup: Waiting for API server pods to reach same revision (this may take 15-20 minutes)")
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
			t.Logf("Cleanup: API server pods are on same revision")

			// Wait for cluster operator to finish progressing
			waitForKubeAPIServerClusterOperatorStable(t, configClient)

			// Verify configmap has been reset to single public key
			// Get the CURRENT operator secret (not the old one from test start)
			t.Logf("Cleanup: Verifying configmap has single public key")
			currentOperatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
			require.NoError(t, err)
			currentPublicKey := currentOperatorSecret.Data[tokenctl.PublicKeyKey]

			checkCertConfigMap(t, kubeClient, map[string]string{
				"service-account-001.pub": string(currentPublicKey),
			})
			t.Logf("Cleanup: Successfully verified cluster is in stable state")
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

		// Wait for rollout to complete before checking operand secret and doing cleanup
		// The operand secret is only updated after the new key is rolled out to all nodes
		t.Logf("Waiting for API server pods to reach same revision after operator secret deletion")
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
		t.Logf("API server pods are on same revision")

		// Wait for cluster operator to finish progressing
		waitForKubeAPIServerClusterOperatorStable(t, configClient)

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

// waitForKubeAPIServerClusterOperatorStable waits for the kube-apiserver cluster operator
// to become stable (Available=True, Progressing=False).
// TODO: Replace with library-go helper once https://github.com/openshift/library-go/pull/2050 merges
func waitForKubeAPIServerClusterOperatorStable(t *testing.T, configClient *configclient.ConfigV1Client) {
	t.Logf("Waiting for kube-apiserver cluster operator to become stable")
	err := wait.PollImmediate(5*time.Second, 5*time.Minute, func() (done bool, err error) {
		co, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
		if err != nil {
			t.Logf("Failed to get kube-apiserver cluster operator: %v", err)
			return false, nil
		}

		// Check Available=True
		availableCond := configv1helpers.FindStatusCondition(co.Status.Conditions, configv1.OperatorAvailable)
		if availableCond == nil || availableCond.Status != configv1.ConditionTrue {
			t.Logf("kube-apiserver not yet available")
			return false, nil
		}

		// Check Progressing=False
		progressingCond := configv1helpers.FindStatusCondition(co.Status.Conditions, configv1.OperatorProgressing)
		if progressingCond == nil || progressingCond.Status != configv1.ConditionFalse {
			t.Logf("kube-apiserver still progressing: %s", progressingCond.Message)
			return false, nil
		}

		return true, nil
	})
	require.NoError(t, err)
	t.Logf("kube-apiserver cluster operator is stable")
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
