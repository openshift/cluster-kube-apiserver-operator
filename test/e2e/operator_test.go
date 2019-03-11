package e2e

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestOperatorNamespace(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	coreV1Client := corev1.NewForConfigOrDie(kubeConfig)
	_, err = coreV1Client.Namespaces().Get("openshift-kube-apiserver-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
}
