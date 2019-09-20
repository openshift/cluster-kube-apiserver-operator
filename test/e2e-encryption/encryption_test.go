package e2e_encryption

import (
	"testing"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	kv, done := testEncryptionType(t, configv1.EncryptionTypeIdentity)
	defer done()

	test.AssertEtcdSecretNotEncrypted(t, kv, metav1.NamespaceSystem, "kubeadmin")
}

func TestEncryptionTypeAESCBC(t *testing.T) {
	kv, done := testEncryptionType(t, configv1.EncryptionTypeAESCBC)
	defer done()

	test.AssertEtcdSecretEncrypted(t, kv, metav1.NamespaceSystem, "kubeadmin", "aescbc")
}

func testEncryptionType(t *testing.T, encryptionType configv1.EncryptionType) (clientv3.KV, func()) {
	t.Helper()

	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	configClient := configv1client.NewForConfigOrDie(kubeConfig)
	apiServerClient := configClient.APIServers()

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	apiServer.Spec.Encryption.Type = encryptionType
	_, err = apiServerClient.Update(apiServer)
	require.NoError(t, err)

	// wait for the encryption controllers to notice the change
	// TODO this is probably not sophisticated enough for multi-stage rollouts
	time.Sleep(time.Minute)

	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)

	return test.NewEtcdKVMust(t, kubernetes.NewForConfigOrDie(kubeConfig))
}
