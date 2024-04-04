package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestCertRotationTimeUpgradeable(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	operatorClient, _, err := genericoperatorclient.NewStaticPodOperatorClient(kubeConfig, operatorv1.GroupVersion.WithResource("kubeapiservers"))
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	ctx := context.Background()
	_, operatorStatus, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
	require.NoError(t, err)
	require.True(t, v1helpers.IsOperatorConditionTrue(operatorStatus.Conditions, "CertRotationTimeUpgradeable"))

	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)
	t.Logf("Creating unsupported-cert-rotation-config...")
	_, err = kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Create(context.TODO(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "unsupported-cert-rotation-config"},
		Data:       map[string]string{"base": "2y"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Delete(context.TODO(), "unsupported-cert-rotation-config", metav1.DeleteOptions{})
	}()

	err = wait.PollImmediate(1*time.Second, 5*time.Second, func() (bool, error) {
		_, operatorStatus, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
		if err != nil {
			return false, err
		}
		clusteroperator, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		certRotationCondition := v1helpers.FindOperatorCondition(operatorStatus.Conditions, "CertRotationTimeUpgradeable")
		upgradeableCondition := configv1helpers.FindStatusCondition(clusteroperator.Status.Conditions, "Upgradeable")
		if certRotationCondition == nil || upgradeableCondition == nil {
			return false, fmt.Errorf("Couldn't find CertRotationTimeUpgradeable or Upgradeable condition")
		}
		if certRotationCondition.Status == operatorv1.ConditionFalse &&
			upgradeableCondition.Status == configv1.ConditionFalse && strings.Contains(upgradeableCondition.Reason, "CertRotationTime") {
			return true, nil
		}
		t.Logf("\nCertRotationTimeUpgradeable: %#v\nUpgradeable: %#v", certRotationCondition, upgradeableCondition)
		return false, nil
	})
	require.NoError(t, err)

	t.Logf("Removing unsupported-cert-rotation-config...")
	err = kubeClient.CoreV1().ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Delete(context.TODO(), "unsupported-cert-rotation-config", metav1.DeleteOptions{})
	require.NoError(t, err)

	err = wait.PollImmediate(1*time.Second, 5*time.Second, func() (bool, error) {
		_, operatorStatus, _, err := operatorClient.GetStaticPodOperatorStateWithQuorum(ctx)
		if err != nil {
			return false, err
		}
		clusteroperator, err := configClient.ClusterOperators().Get(context.TODO(), "kube-apiserver", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		certRotationCondition := v1helpers.FindOperatorCondition(operatorStatus.Conditions, "CertRotationTimeUpgradeable")
		upgradeableCondition := configv1helpers.FindStatusCondition(clusteroperator.Status.Conditions, "Upgradeable")
		if certRotationCondition == nil || upgradeableCondition == nil {
			return false, fmt.Errorf("Couldn't find CertRotationTimeUpgradeable or Upgradeable condition")
		}
		if certRotationCondition.Status == operatorv1.ConditionTrue &&
			(upgradeableCondition.Status == configv1.ConditionTrue || !strings.Contains(upgradeableCondition.Reason, "CertRotationTime")) {
			return true, nil
		}
		t.Logf("\nCertRotationTimeUpgradeable: %#v\nUpgradeable: %#v", certRotationCondition, upgradeableCondition)
		return false, nil
	})
	require.NoError(t, err)
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
			Type:       "SecretTypeTLS",
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
