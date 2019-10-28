package encryption

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	library "github.com/openshift/library-go/test/library/encryption"
)

var DefaultTargetGRs = []schema.GroupResource{
	{Group: "", Resource: "secrets"},
	{Group: "", Resource: "configmaps"},
}

func AssertSecretsAndConfigMaps(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertSecrets(t, clientSet.Etcd, string(expectedMode))
	assertConfigMaps(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, DefaultTargetGRs, namespace, labelSelector)
}

func assertSecrets(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all Secrets where encrypted/decrypted for %q mode", expectedMode)
	totalSecrets, err := library.VerifyResources(t, etcdClient, "/kubernetes.io/secrets/", expectedMode)
	t.Logf("Verified %d Secrets, err %v", totalSecrets, err)
	require.NoError(t, err)
}

func assertConfigMaps(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all ConfigMaps where encrypted/decrypted for %q mode", expectedMode)
	totalConfigMaps, err := library.VerifyResources(t, etcdClient, "/kubernetes.io/configmaps/", expectedMode)
	t.Logf("Verified %d ConfigMaps, err %v", totalConfigMaps, err)
	require.NoError(t, err)
}
