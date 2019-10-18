package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
)

func TestEncryptionTypeAESCBC(t *testing.T) {
	e := encryption.NewE(t)
	etcdClient := testEncryptionType(e, configv1.EncryptionTypeAESCBC)
	encryption.AssertSecretsAndConfigMaps(e, etcdClient, string(configv1.EncryptionTypeAESCBC))
}

func testEncryptionType(t testing.TB, encryptionType configv1.EncryptionType) encryption.EtcdClient {
	t.Helper()
	t.Logf("Starting encryption e2e test for %q mode", encryptionType)

	etcdClient, apiServerClient, operatorClient := encryption.GetClients(t)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	needsUpdate := apiServer.Spec.Encryption.Type != encryptionType
	if needsUpdate {
		t.Logf("Updating encryption type in the config file for APIServer to %q", encryptionType)
		apiServer.Spec.Encryption.Type = encryptionType
		_, err = apiServerClient.Update(apiServer)
		require.NoError(t, err)
	} else {
		t.Logf("APIServer is already configured to use %q mode", encryptionType)
	}

	encryption.WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded(t, operatorClient)
	return etcdClient
}
