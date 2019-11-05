package encryption

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/coreos/etcd/clientv3"
	"github.com/stretchr/testify/require"

	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	library "github.com/openshift/library-go/test/library/encryption"
)

func GetOperator(t testing.TB) operatorv1client.KubeAPIServerInterface {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return operatorClient.KubeAPIServers()
}

func GetRawSecretOfLife(t testing.TB, clientSet library.ClientSet, namespace string) string {
	t.Helper()
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	secretOfLifeEtcdPrefix := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", namespace, "secret-of-life")
	resp, err := clientSet.Etcd.Get(timeout, secretOfLifeEtcdPrefix, clientv3.WithPrefix())
	require.NoError(t, err)

	if len(resp.Kvs) != 1 {
		t.Errorf("Expected to get a single key from etcd, got %d", len(resp.Kvs))
	}

	return string(resp.Kvs[0].Value)
}

func CreateAndStoreSecretOfLife(t testing.TB, clientSet library.ClientSet, namespace string) runtime.Object {
	t.Helper()
	{
		oldSecretOfLife, err := clientSet.Kube.CoreV1().Secrets(namespace).Get("secret-of-life", metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			t.Errorf("Failed to check if the secret already exists, due to %v", err)
		}
		if len(oldSecretOfLife.Name) > 0 {
			t.Log("The secret already exist, removing it first")
			err := clientSet.Kube.CoreV1().Secrets(namespace).Delete(oldSecretOfLife.Name, &metav1.DeleteOptions{})
			if err != nil {
				t.Errorf("Failed to delete %s, err %v", oldSecretOfLife.Name, err)
			}
		}
	}
	t.Logf("Creating %q in %s namespace", "secret-of-life", namespace)
	rawSecretOfLife := SecretOfLife(t, namespace)
	secretOfLife, err := clientSet.Kube.CoreV1().Secrets(namespace).Create(rawSecretOfLife.(*corev1.Secret))
	require.NoError(t, err)
	return secretOfLife
}

func SecretOfLife(t testing.TB, namespace string) runtime.Object {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret-of-life",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"quote": []byte("I have no special talents. I am only passionately curious"),
		},
	}
}
