package encryption

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	OASTargetNamespace   = "openshift-apiserver"
	OASOperatorNamespace = "openshift-apiserver-operator"
)

var OASTargetGRs = []schema.GroupResource{
	{Group: "", Resource: "secrets"},
	{Group: "", Resource: "configmaps"},
}

var OASLabelSelector = "encryption.apiserver.operator.openshift.io/component=openshift-apiserver"
var OASEncryptionConfigSecretName = fmt.Sprintf("encryption-config-%s", OASTargetNamespace)

func GetRawOASSecretOfLife(t testing.TB, clientSet library.ClientSet, namespace string) string {
	t.Helper()
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	prefix := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", namespace, "oas-secret-of-life")
	resp, err := clientSet.Etcd.Get(timeout, prefix, clientv3.WithPrefix())
	require.NoError(t, err)
	if len(resp.Kvs) != 1 {
		t.Errorf("Expected to get a single key from etcd, got %d", len(resp.Kvs))
	}
	return string(resp.Kvs[0].Value)
}

func CreateAndStoreOASSecretOfLife(t testing.TB, clientSet library.ClientSet, namespace string) runtime.Object {
	t.Helper()

	oldSecret, err := clientSet.Kube.CoreV1().Secrets(namespace).Get(context.TODO(), "oas-secret-of-life", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		t.Errorf("Failed to check if the secret already exists: %v", err)
	}
	if len(oldSecret.Name) > 0 {
		t.Log("The OAS secret already exists, removing it first")
		err := clientSet.Kube.CoreV1().Secrets(namespace).Delete(context.TODO(), oldSecret.Name, metav1.DeleteOptions{})
		if err != nil {
			t.Errorf("Failed to delete %s: %v", oldSecret.Name, err)
		}
	}

	t.Logf("Creating %q in %s namespace", "oas-secret-of-life", namespace)
	raw := OASSecretOfLife(t, namespace)
	secret, err := clientSet.Kube.CoreV1().Secrets(namespace).Create(context.TODO(), raw.(*corev1.Secret), metav1.CreateOptions{})
	require.NoError(t, err)
	return secret
}

func OASSecretOfLife(t testing.TB, namespace string) runtime.Object {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oas-secret-of-life",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"quote": []byte("The only way to do great work is to love what you do"),
		},
	}
}

func AssertOASSecretOfLifeEncrypted(t testing.TB, clientSet library.ClientSet, raw runtime.Object) {
	t.Helper()
	secret := raw.(*corev1.Secret)
	rawValue := GetRawOASSecretOfLife(t, clientSet, secret.Namespace)
	if strings.Contains(rawValue, string(secret.Data["quote"])) {
		t.Errorf("The OAS secret in etcd contains plain text %q, content: %s", string(secret.Data["quote"]), rawValue)
	}
}

func AssertOASSecretOfLifeNotEncrypted(t testing.TB, clientSet library.ClientSet, raw runtime.Object) {
	t.Helper()
	secret := raw.(*corev1.Secret)
	rawValue := GetRawOASSecretOfLife(t, clientSet, secret.Namespace)
	if !strings.Contains(rawValue, string(secret.Data["quote"])) {
		t.Errorf("The OAS secret in etcd doesn't have %q, content: %s", string(secret.Data["quote"]), rawValue)
	}
}

func AssertOASSecretsAndConfigMaps(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertSecrets(t, clientSet.Etcd, string(expectedMode))
	assertConfigMaps(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, OASTargetGRs, namespace, labelSelector)
}
