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
	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Helper to get current revision from KubeAPIServer status
	getCurrentRevision := func() (int32, error) {
		kas, err := operatorClient.KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		var maxRevision int32
		for _, ns := range kas.Status.NodeStatuses {
			if ns.CurrentRevision > maxRevision {
				maxRevision = ns.CurrentRevision
			}
		}
		return maxRevision, nil
	}

	// Log initial revision
	initialRevision, err := getCurrentRevision()
	require.NoError(t, err)
	t.Logf("REVISION DEBUG: Initial revision before any tests: %d", initialRevision)

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// The operand secret should be recreated after deletion.
	t.Run("operand-secret-deletion", func(t *testing.T) {
		revisionBefore, _ := getCurrentRevision()
		t.Logf("REVISION DEBUG: [operand-secret-deletion] Revision BEFORE: %d", revisionBefore)

		err := kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)

		revisionAfter, _ := getCurrentRevision()
		t.Logf("REVISION DEBUG: [operand-secret-deletion] Revision AFTER: %d (delta: %d)", revisionAfter, revisionAfter-revisionBefore)
	})

	// The operand config map should be recreated after deletion.
	// Note: it will roll out a new version
	t.Run("configmap-deletion", func(t *testing.T) {
		revisionBefore, _ := getCurrentRevision()
		t.Logf("REVISION DEBUG: [configmap-deletion] Revision BEFORE: %d", revisionBefore)

		err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
		})

		t.Logf("REVISION DEBUG: [configmap-deletion] Revision after configmap recreated (before wait): %d", func() int32 { r, _ := getCurrentRevision(); return r }())
		revisionAfter, _ := getCurrentRevision()
		t.Logf("REVISION DEBUG: [configmap-deletion] Revision AFTER stabilization: %d (delta: %d)", revisionAfter, revisionAfter-revisionBefore)
	})

	// The secret in the operator namespace should be recreated with a new keypair
	// after deletion. The configmap in the operand namespace should be updated
	// immediately, and the secret once the configmap is present on all nodes.
	//
	// Note: it will roll out a new version - potentially multiple times
	t.Run("operator-secret-deletion", func(t *testing.T) {
		revisionBefore, _ := getCurrentRevision()
		t.Logf("REVISION DEBUG: [operator-secret-deletion] Revision BEFORE: %d", revisionBefore)

		// Delete the operator secret
		err := kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)

		t.Logf("REVISION DEBUG: [operator-secret-deletion] After secret delete, revision: %d", func() int32 { r, _ := getCurrentRevision(); return r }())

		// deletion triggers a roll-out - wait until a new version has been rolled out
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

		t.Logf("REVISION DEBUG: [operator-secret-deletion] After WaitForAPIServerToStabilizeOnTheSameRevision, revision: %d", func() int32 { r, _ := getCurrentRevision(); return r }())

		// Wait for ALL nodes to reach the same revision
		t.Log("REVISION DEBUG: [operator-secret-deletion] Waiting for all nodes to reach same revision...")
		waitForAllNodesToReachSameRevision(t, operatorClient)

		t.Logf("REVISION DEBUG: [operator-secret-deletion] After full first stabilization, revision: %d", func() int32 { r, _ := getCurrentRevision(); return r }())

		// Ensure that the cert configmap is always removed at the end of the test
		// to ensure it will contain only the current public key. This property is
		// essential to allowing repeated invocations of the containing test.
		defer func() {
			t.Logf("REVISION DEBUG: [operator-secret-deletion] DEFER: Before configmap delete, revision: %d", func() int32 { r, _ := getCurrentRevision(); return r }())

			err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
			require.NoError(t, err)

			t.Logf("REVISION DEBUG: [operator-secret-deletion] DEFER: After configmap delete, revision: %d", func() int32 { r, _ := getCurrentRevision(); return r }())

			// Cleanup deletion also triggers a rollout - wait for stabilization
			testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

			revisionAfterDefer, _ := getCurrentRevision()
			t.Logf("REVISION DEBUG: [operator-secret-deletion] DEFER: After WaitForAPIServerToStabilizeOnTheSameRevision, revision: %d", revisionAfterDefer)

			// Wait for ALL nodes to reach the same revision (cluster operator to stop progressing)
			t.Log("REVISION DEBUG: [operator-secret-deletion] DEFER: Waiting for cluster operator to stop progressing...")
			waitForAllNodesToReachSameRevision(t, operatorClient)

			finalRevision, _ := getCurrentRevision()
			t.Logf("REVISION DEBUG: [operator-secret-deletion] DEFER: After full stabilization, revision: %d (total delta from start: %d)", finalRevision, finalRevision-revisionBefore)
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

// waitForAllNodesToReachSameRevision waits until all nodes have the same current revision.
// This ensures the rollout is complete across all control plane nodes.
func waitForAllNodesToReachSameRevision(t *testing.T, operatorClient *operatorv1client.OperatorV1Client) {
	err := wait.PollImmediate(5*time.Second, 20*time.Minute, func() (done bool, err error) {
		kas, err := operatorClient.KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			t.Logf("error getting KubeAPIServer: %v", err)
			return false, nil
		}

		if len(kas.Status.NodeStatuses) == 0 {
			t.Log("no node statuses found")
			return false, nil
		}

		// Check if all nodes have the same current revision
		firstRevision := kas.Status.NodeStatuses[0].CurrentRevision
		allSame := true
		for _, ns := range kas.Status.NodeStatuses {
			if ns.CurrentRevision != firstRevision {
				allSame = false
				t.Logf("nodes not yet on same revision: node %s at revision %d (expected %d)", ns.NodeName, ns.CurrentRevision, firstRevision)
				break
			}
			// Also check target revision matches current (no pending rollout)
			if ns.TargetRevision != 0 && ns.TargetRevision != ns.CurrentRevision {
				allSame = false
				t.Logf("node %s has pending rollout: current=%d, target=%d", ns.NodeName, ns.CurrentRevision, ns.TargetRevision)
				break
			}
		}

		if allSame {
			t.Logf("all %d nodes are at revision %d", len(kas.Status.NodeStatuses), firstRevision)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "timed out waiting for all nodes to reach the same revision")
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
