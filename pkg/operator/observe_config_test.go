package operator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestObserveClusterConfig(t *testing.T) {
	const (
		podCIDR     = "10.9.8.7/99"
		serviceCIDR = "11.6.7.5/88"
	)
	kubeClient := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: " + podCIDR + "\n  serviceCIDR: " + serviceCIDR + "\n",
		},
	})
	result, err := observeClusterConfig(kubeClient, &rest.Config{}, map[string]interface{}{})
	if err != nil {
		t.Error("expected err == nil")
	}
	restrictedCIDRs, _, err := unstructured.NestedSlice(result, "admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedCIDRs[0] != podCIDR {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != serviceCIDR {
		t.Error(restrictedCIDRs[1])
	}
}
