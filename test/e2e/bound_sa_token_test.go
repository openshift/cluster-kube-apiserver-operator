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
func TestBoundTokenSignerController(t *testing.T) {
	kubeConfig, err := testlibrary.NewClientConfigForTest(t)
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	require.NoError(t, err)

	targetNamespace := operatorclient.TargetNamespace
	operatorNamespace := operatorclient.OperatorNamespace

	// Retrieve the operator secret. The values in the secret and config map in the
	// operand namespace should match the values in the operator secret.
	operatorSecret, err := kubeClient.Secrets(operatorNamespace).Get(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	operatorPublicKey := operatorSecret.Data[tokenctl.PublicKeyKey]
	operatorPrivateKey := operatorSecret.Data[tokenctl.PrivateKeyKey]

	// The operand secret should be recreated after deletion.
	t.Run("operand-secret-deletion", func(t *testing.T) {
		t.Skip()
		err := kubeClient.Secrets(targetNamespace).Delete(context.TODO(), tokenctl.SigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkBoundTokenOperandSecret(t, kubeClient, regularTimeout, operatorSecret.Data)
	})

	// The operand config map should be recreated after deletion.
	// Note: it will roll out a new version
	t.Run("configmap-deletion", func(t *testing.T) {
		t.Skip()
		err := kubeClient.ConfigMaps(targetNamespace).Delete(context.TODO(), tokenctl.PublicKeyConfigMapName, metav1.DeleteOptions{})
		require.NoError(t, err)
		checkCertConfigMap(t, kubeClient, map[string]string{
			"service-account-001.pub": string(operatorPublicKey),
		})

		// deletion triggers a roll-out - wait until a new version has been rolled out
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))
	})

	// The secret in the operator namespace should be recreated with a new keypair
	// after deletion. The configmap in the operand namespace should be updated
	// immediately, and the secret once the configmap is present on all nodes.
	//
	// Note: it will roll out a new version
	t.Run("operator-secret-deletion", func(t *testing.T) {
		t.Skip()
		// Delete the operator secret
		err := kubeClient.Secrets(operatorNamespace).Delete(context.TODO(), tokenctl.NextSigningKeySecretName, metav1.DeleteOptions{})
		require.NoError(t, err)

		// deletion triggers a roll-out - wait until a new version has been rolled out
		testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

		// Ensure that the cert configmap is always removed at the end of the test
		// to ensure it will contain only the current public key. This property is
		// essential to allowing repeated invocations of the containing test.
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
