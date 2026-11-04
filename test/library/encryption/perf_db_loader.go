package encryption

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	ConfigMapStatsKey = "created configmaps"
	SecretsStatsKey   = "created secrets"

	DefaultDBLoaderWorkers = 3
	DefaultMinSecrets      = 25000
	DefaultMinConfigMaps   = 14000
	DefaultMaxMigration    = 28 * time.Minute
)

// DefaultDBLoader populates etcd with secrets/configmaps for encryption perf tests.
func DefaultDBLoader() library.DBLoaderFuncType {
	return library.DBLoaderRepeat(1, true,
		CreateNamespace,
		WaitUntilNamespaceActive,
		library.DBLoaderRepeatParallel(5010, 50, false, CreateConfigMap, ReportConfigMap),
		library.DBLoaderRepeatParallel(9010, 50, false, CreateSecret, ReportSecret),
	)
}

func AssertDBPopulated(t testing.TB, _ map[string]int, statStore map[string]int) {
	t.Helper()
	secretsCount, ok := statStore[SecretsStatsKey]
	if !ok {
		require.NoError(t, errors.New("missing secrets count stats, can't continue the test"))
	}
	if secretsCount < DefaultMinSecrets {
		require.NoError(t, fmt.Errorf("expected to create at least %d secrets but %d were created", DefaultMinSecrets, secretsCount))
	}
	t.Logf("Created %d secrets", secretsCount)

	configMapsCount, ok := statStore[ConfigMapStatsKey]
	if !ok {
		require.NoError(t, errors.New("missing configmaps count stats, can't continue the test"))
	}
	if configMapsCount < DefaultMinConfigMaps {
		require.NoError(t, fmt.Errorf("expected to create at least %d configmaps but %d were created", DefaultMinConfigMaps, configMapsCount))
	}
	t.Logf("Created %d configmaps", configMapsCount)
}

func AssertMigrationTime(t testing.TB, migrationTime time.Duration) {
	t.Helper()
	t.Logf("migration took %v", migrationTime)
	if migrationTime > DefaultMaxMigration {
		t.Errorf("migration took too long (%v), expected it to take no more than %v", migrationTime, DefaultMaxMigration)
	}
}

func CreateSecret(kubeClient kubernetes.Interface, namespace string, _ func(error), _ func(string)) error {
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

func ReportSecret(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(SecretsStatsKey)
	return nil
}

func CreateConfigMap(kubeClient kubernetes.Interface, namespace string, _ func(error), _ func(string)) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "encryption-",
		},
	}
	_, err := kubeClient.CoreV1().ConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{})
	return err
}

func ReportConfigMap(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(ConfigMapStatsKey)
	return nil
}

func CreateNamespace(kubeClient kubernetes.Interface, name string, _ func(error), _ func(string)) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	_, err := kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	return err
}

func WaitUntilNamespaceActive(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), _ func(string)) error {
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
