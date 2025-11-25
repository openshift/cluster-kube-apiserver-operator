package e2e_encryption_perf

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	cmStatsKey      = "created configmaps"
	secretsStatsKey = "created secrets"
)

var provider = flag.String("provider", "aescbc", "encryption provider used by the tests")

func TestPerfEncryption(tt *testing.T) {
	operatorClient := operatorencryption.GetOperator(tt)
	library.TestPerfEncryption(tt, library.PerfScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		GetOperatorConditionsFunc: func(t testing.TB) ([]operatorv1.OperatorCondition, error) {
			apiServerOperator, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return apiServerOperator.Status.Conditions, nil
		},
		AssertDBPopulatedFunc: func(t testing.TB, errorStore map[string]int, statStore map[string]int) {
			secretsCount, ok := statStore[secretsStatsKey]
			if !ok {
				err := errors.New("missing secrets count stats, can't continue the test")
				require.NoError(t, err)
			}
			if secretsCount < 25000 {
				err := fmt.Errorf("expected to create at least 25000 secrets but %d were created", secretsCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d secrets", secretsCount)

			configMpasCount, ok := statStore[cmStatsKey]
			if !ok {
				err := errors.New("missing configmaps count stats, can't continue the test")
				require.NoError(t, err)
			}
			if configMpasCount < 14000 {
				err := fmt.Errorf("expected to create at least 14000 configmaps but %d were created", configMpasCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d configmaps", configMpasCount)

		},
		AssertMigrationTime: func(t testing.TB, migrationTime time.Duration) {
			t.Logf("migration took %v", migrationTime)
			expectedMigrationTime := 28 * time.Minute
			if migrationTime > expectedMigrationTime {
				t.Errorf("migration took too long (%v), expected it to take no more than %v", migrationTime, expectedMigrationTime)
			}
		},
		DBLoaderWorkers: 3,
		DBLoaderFunc: library.DBLoaderRepeat(1, true,
			createNamespace,
			waitUntilNamespaceActive,
			library.DBLoaderRepeatParallel(5010, 50, false, createConfigMap, reportConfigMap),
			library.DBLoaderRepeatParallel(9010, 50, false, createSecret, reportSecret)),
		EncryptionProvider: configv1.EncryptionType(*provider),
	})
}

func createSecret(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "encryption-",
		},
		Data: map[string][]byte{
			"quote": []byte("I have no special talents. I am only passionately curious"),
		},
	}
	_, err := kubeClient.CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	return err
}

func reportSecret(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(secretsStatsKey)
	return nil
}

func createConfigMap(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "encryption-",
		},
		Data:       nil,
		BinaryData: nil,
	}

	_, err := kubeClient.CoreV1().ConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{})
	return err
}

func reportConfigMap(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(cmStatsKey)
	return nil
}

func createNamespace(kubeClient kubernetes.Interface, name string, errorCollector func(error), statsCollector func(string)) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		},
		Status: corev1.NamespaceStatus{},
	}
	_, err := kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	return err
}

func waitUntilNamespaceActive(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	err := wait.Poll(10*time.Millisecond, 30*time.Second, func() (bool, error) {
		ns, err := kubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if ns.Status.Phase == corev1.NamespaceActive {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		err = fmt.Errorf("failed waiting for ns to become ready, err %v", err)
		errorCollector(err)
	}
	return err
}
