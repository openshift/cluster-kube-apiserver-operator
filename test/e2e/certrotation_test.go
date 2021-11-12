package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

func TestCertRotationTimeUpgradeable(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	operatorClient, _, err := genericoperatorclient.NewStaticPodOperatorClient(kubeConfig, operatorv1.GroupVersion.WithResource("kubeapiservers"))
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	ctx := context.TODO()
	_, operatorStatus, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionTrue(operatorStatus.Conditions, "CertRotationTimeUpgradeable"))

	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)
	_, err = kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Create(context.TODO(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "unsupported-cert-rotation-config"},
		Data:       map[string]string{"base": "2y"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Delete(context.TODO(), "unsupported-cert-rotation-config", metav1.DeleteOptions{})
	}()

	// TODO better detection maybe someday
	time.Sleep(5 * time.Second)

	_, operatorStatus, _, err = operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionFalse(operatorStatus.Conditions, "CertRotationTimeUpgradeable"))
	clusteroperator, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, configv1helpers.IsStatusConditionFalse(clusteroperator.Status.Conditions, "Upgradeable"))

	err = kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Delete(context.TODO(), "unsupported-cert-rotation-config", metav1.DeleteOptions{})
	require.NoError(t, err)
	// TODO better detection maybe someday
	time.Sleep(5 * time.Second)

	_, operatorStatus, _, err = operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionTrue(operatorStatus.Conditions, "CertRotationTimeUpgradeable"))
	clusteroperator, err = configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, configv1helpers.IsStatusConditionTrue(clusteroperator.Status.Conditions, "Upgradeable"))

}

func TestCertRotationStompOnBadType(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)

	// this is inherently racy against a controller
	err = wait.PollImmediate(10*time.Millisecond, 5*time.Second, func() (done bool, err error) {
		if err := kubeClient.CoreV1().Secrets(operatorclient.OperatorNamespace).Delete(context.TODO(), "aggregator-client-signer", metav1.DeleteOptions{}); err != nil {
			return false, nil
		}
		if _, err := kubeClient.CoreV1().Secrets(operatorclient.OperatorNamespace).Create(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.OperatorNamespace, Name: "aggregator-client-signer"},
			Type:       "Opaque",
		}, metav1.CreateOptions{}); err != nil {
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)

	err = wait.PollImmediate(100*time.Millisecond, 30*time.Second, func() (done bool, err error) {
		curr, err := kubeClient.CoreV1().Secrets(operatorclient.OperatorNamespace).Get(context.TODO(), "aggregator-client-signer", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if curr.Type == corev1.SecretTypeTLS {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)
}
