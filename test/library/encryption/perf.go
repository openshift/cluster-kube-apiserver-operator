package encryption

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// Stats keys the perf DB loader helpers record.
const (
	CMStatsKey      = "created configmaps"
	SecretsStatsKey = "created secrets"
)

// CreateNamespace creates the namespace the DB loader populates.
func CreateNamespace(kubeClient kubernetes.Interface, name string, errorCollector func(error), statsCollector func(string)) error {
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

// WaitUntilNamespaceActive waits until the given namespace is active, so the
// DB loader can populate it.
func WaitUntilNamespaceActive(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
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

// CreateSecret creates a secret with a fixed payload in the given namespace.
func CreateSecret(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
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

// ReportSecret records a created secret in the stats store.
func ReportSecret(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(SecretsStatsKey)
	return nil
}

// CreateConfigMap creates an empty configmap in the given namespace.
func CreateConfigMap(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
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

// ReportConfigMap records a created configmap in the stats store.
func ReportConfigMap(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(CMStatsKey)
	return nil
}
