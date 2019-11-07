package encryption

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	library "github.com/openshift/library-go/test/library/encryption"
)

var DefaultTargetGRs = []schema.GroupResource{
	{Group: "", Resource: "secrets"},
	{Group: "", Resource: "configmaps"},
}

func AssertSecretOfLifeNotEncrypted(t testing.TB, clientSet library.ClientSet, rawSecretOfLife runtime.Object) {
	t.Helper()
	secretOfLife := rawSecretOfLife.(*corev1.Secret)
	rawSecretValue := GetRawSecretOfLife(t, clientSet, secretOfLife.Namespace)
	if !strings.Contains(rawSecretValue, string(secretOfLife.Data["quote"])) {
		t.Errorf("The secret received from etcd doesn't have %q, content of the secret (etcd) %s", string(secretOfLife.Data["quote"]), rawSecretValue)
	}
}

func AssertSecretOfLifeEncrypted(t testing.TB, clientSet library.ClientSet, rawSecretOfLife runtime.Object) {
	t.Helper()
	secretOfLife := rawSecretOfLife.(*corev1.Secret)
	rawSecretValue := GetRawSecretOfLife(t, clientSet, secretOfLife.Namespace)
	if strings.Contains(rawSecretValue, string(secretOfLife.Data["quote"])) {
		t.Errorf("The secret received from etcd have %q (plain text), content of the secret (etcd) %s", string(secretOfLife.Data["quote"]), rawSecretValue)
	}
}

func AssertSecretsAndConfigMaps(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertSecrets(t, clientSet.Etcd, string(expectedMode))
	assertConfigMaps(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, DefaultTargetGRs, namespace, labelSelector)
}

func assertSecrets(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all Secrets where encrypted/decrypted for %q mode", expectedMode)
	totalSecrets, err := library.VerifyResources(t, etcdClient, "/kubernetes.io/secrets/", expectedMode, false)
	t.Logf("Verified %d Secrets", totalSecrets)
	require.NoError(t, err)
}

func assertConfigMaps(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all ConfigMaps where encrypted/decrypted for %q mode", expectedMode)
	totalConfigMaps, err := library.VerifyResources(t, etcdClient, "/kubernetes.io/configmaps/", expectedMode, false)
	t.Logf("Verified %d ConfigMaps", totalConfigMaps)
	require.NoError(t, err)
}
