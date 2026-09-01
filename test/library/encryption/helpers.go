package encryption

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func GetOperator(t testing.TB) operatorv1client.KubeAPIServerInterface {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return operatorClient.KubeAPIServers()
}

// EncryptionTypeFromEnv returns the encryption type from ENCRYPTION_PROVIDER
// (set by Makefile or OTE/CI). This is the single configuration path for e2e
// encryption provider selection.
func EncryptionTypeFromEnv(t testing.TB) configv1.EncryptionType {
	t.Helper()
	env := os.Getenv("ENCRYPTION_PROVIDER")
	if env == "" {
		t.Fatal("ENCRYPTION_PROVIDER env var is required (e.g. aescbc, aesgcm)")
	}
	return configv1.EncryptionType(env)
}
