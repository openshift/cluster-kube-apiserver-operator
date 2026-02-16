package e2e

import (
	"context"
	"fmt"
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
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	tokenctl "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
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
// Note: this test will roll out a new version - multiple times
// TODO: CNTRLPLANE-2223 - Migrate this test to OTE ginkgo framework
func TestBoundTokenSignerController(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Helper to log current revision info
	logRevisionInfo := func(label string) {
		kas, err := operatorClient.KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			t.Logf("[%s] Error getting KubeAPIServer: %v", label, err)
			return
		}
		for _, ns := range kas.Status.NodeStatuses {
			t.Logf("[%s] Node %s: CurrentRevision=%d, TargetRevision=%d", label, ns.NodeName, ns.CurrentRevision, ns.TargetRevision)
		}
	}

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// The operand secret should be recreated after deletion.
	t.Run("operand-secret-deletion", func(t *testing.T) {
		err := kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
	})

	// The operand config map should be recreated after deletion.
	// Note: it will roll out a new version
	t.Run("configmap-deletion", func(t *testing.T) {
		err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
		})
	})

	// The secret in the operator namespace should be recreated with a new keypair
	// after deletion. The configmap in the operand namespace should be updated
	// immediately, and the secret once the configmap is present on all nodes.
	//
	// Note: it will roll out a new version
	t.Run("operator-secret-deletion", func(t *testing.T) {
		logRevisionInfo("BEFORE_SECRET_DELETE")

		// Delete the operator secret
		t.Log(">>> Deleting operator secret...")
		err := kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)
		t.Log(">>> Operator secret deleted")

		logRevisionInfo("AFTER_SECRET_DELETE")

		// deletion triggers a roll-out - wait until a new version has been rolled out
		t.Log(">>> Starting WaitForAPIServerToStabilizeOnTheSameRevision (after secret deletion)...")
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
		t.Log(">>> WaitForAPIServerToStabilizeOnTheSameRevision completed (after secret deletion)")

		logRevisionInfo("AFTER_FIRST_STABILIZATION")

		// Ensure that the cert configmap is always removed at the end of the test
		// to ensure it will contain only the current public key. This property is
		// essential to allowing repeated invocations of the containing test.
		defer func() {
			logRevisionInfo("DEFER_BEFORE_CONFIGMAP_DELETE")

			t.Log(">>> DEFER: Deleting cert configmap...")
			err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
			require.NoError(t, err)
			t.Log(">>> DEFER: Cert configmap deleted")

			logRevisionInfo("DEFER_AFTER_CONFIGMAP_DELETE")

			// Cleanup deletion also triggers a rollout - wait for stabilization
			t.Log(">>> DEFER: Starting first WaitForAPIServerToStabilizeOnTheSameRevision...")
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
			t.Log(">>> DEFER: First WaitForAPIServerToStabilizeOnTheSameRevision completed")

			// Cleanup deletion also triggers a rollout - wait for stabilization
			t.Log(">>> DEFER: Starting first WaitForAPIServerToStabilizeOnTheSameRevision...")
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
			t.Log(">>> DEFER: First WaitForAPIServerToStabilizeOnTheSameRevision completed")

			// Cleanup deletion also triggers a rollout - wait for stabilization
			t.Log(">>> DEFER: Starting first WaitForAPIServerToStabilizeOnTheSameRevision...")
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
			t.Log(">>> DEFER: First WaitForAPIServerToStabilizeOnTheSameRevision completed")

			logRevisionInfo("DEFER_AFTER_FIRST_POD_STABILIZATION")

			// Wait for cluster operator to fully settle
			t.Log(">>> DEFER: Starting waitForClusterOperatorStable...")
			waitForClusterOperatorStable(t, configClient, operatorClient)
			t.Log(">>> DEFER: waitForClusterOperatorStable completed")

			logRevisionInfo("DEFER_FINAL_STATE")
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

// waitForClusterOperatorStable waits until the kube-apiserver cluster operator
// is Available=True, Progressing=False, and Degraded=False.
// This ensures the operator has fully settled after any rollouts.
func waitForClusterOperatorStable(t *testing.T, configClient *configclient.ConfigV1Client, operatorClient *operatorv1client.OperatorV1Client) {
	t.Log("Waiting for kube-apiserver cluster operator to be stable...")

	pollCount := 0
	err := wait.PollImmediate(5*time.Second, 15*time.Minute, func() (done bool, err error) {
		pollCount++

		co, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
		if err != nil {
			t.Logf("[Poll #%d] Error getting ClusterOperator: %v", pollCount, err)
			return false, nil
		}

		available := false
		progressing := true
		degraded := true
		var progressingMsg, degradedMsg string

		for _, cond := range co.Status.Conditions {
			switch cond.Type {
			case configv1.OperatorAvailable:
				available = cond.Status == configv1.ConditionTrue
			case configv1.OperatorProgressing:
				progressing = cond.Status == configv1.ConditionTrue
				progressingMsg = cond.Message
			case configv1.OperatorDegraded:
				degraded = cond.Status == configv1.ConditionTrue
				degradedMsg = cond.Message
			}
		}

		// Log every poll with detailed info
		t.Logf("[Poll #%d] ClusterOperator: Available=%v, Progressing=%v, Degraded=%v", pollCount, available, progressing, degraded)

		if progressing && progressingMsg != "" {
			// Truncate long messages
			msg := progressingMsg
			if len(msg) > 300 {
				msg = msg[:300] + "..."
			}
			t.Logf("[Poll #%d] Progressing reason: %s", pollCount, msg)
		}

		if degraded && degradedMsg != "" {
			msg := degradedMsg
			if len(msg) > 300 {
				msg = msg[:300] + "..."
			}
			t.Logf("[Poll #%d] Degraded reason: %s", pollCount, msg)
		}

		// Also log current revision info when progressing
		if progressing {
			kas, err := operatorClient.KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err == nil {
				for _, ns := range kas.Status.NodeStatuses {
					t.Logf("[Poll #%d] Node %s: CurrentRevision=%d, TargetRevision=%d", pollCount, ns.NodeName, ns.CurrentRevision, ns.TargetRevision)
				}
			}
		}

		if available && !progressing && !degraded {
			t.Logf("[Poll #%d] Cluster operator is stable: Available=True, Progressing=False, Degraded=False", pollCount)
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		// On timeout, dump final state for debugging
		t.Log("=== TIMEOUT DEBUG INFO ===")
		co, coErr := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
		if coErr == nil {
			for _, cond := range co.Status.Conditions {
				t.Logf("ClusterOperator condition: Type=%s, Status=%s, Reason=%s, Message=%s",
					cond.Type, cond.Status, cond.Reason, cond.Message)
			}
		}
		kas, kasErr := operatorClient.KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if kasErr == nil {
			for _, ns := range kas.Status.NodeStatuses {
				t.Logf("KubeAPIServer NodeStatus: Node=%s, CurrentRevision=%d, TargetRevision=%d, LastFailedRevision=%d, LastFailedReason=%s",
					ns.NodeName, ns.CurrentRevision, ns.TargetRevision, ns.LastFailedRevision, ns.LastFailedReason)
			}
		}
		t.Log("=== END TIMEOUT DEBUG INFO ===")
	}

	require.NoError(t, err, fmt.Sprintf("timed out after %d polls waiting for cluster operator to be stable", pollCount))
}
