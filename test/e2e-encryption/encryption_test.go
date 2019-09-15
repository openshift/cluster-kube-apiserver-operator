package e2e_encryption

import (
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestEncryptionKeyEtcd(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)
	kv, done := test.NewEtcdKVMust(t, kubeClient)
	defer done()

	test.AssertEtcdSecretNotEncrypted(t, kv, metav1.NamespaceSystem, "kubeadmin")
}
