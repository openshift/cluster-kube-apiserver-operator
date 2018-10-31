package operator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
)

func TestObserveClusterConfig(t *testing.T) {
	const (
		podCIDR     = "10.9.8.7/99"
		serviceCIDR = "11.6.7.5/88"
	)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: " + podCIDR + "\n  serviceCIDR: " + serviceCIDR + "\n",
		},
	}
	indexer.Add(obj)

	listers := Listers{
		configmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, err := observeClusterConfig(listers, map[string]interface{}{})
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

func TestObserveRegistryConfig(t *testing.T) {
	const (
		expectedInternalRegistryHostname = "docker-registry.openshift-image-registry.svc.cluster.local:5000"
	)

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	imageConfig := &configv1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: configv1.ImageStatus{
			InternalRegistryHostname: expectedInternalRegistryHostname,
		},
	}
	indexer.Add(imageConfig)

	listers := Listers{
		imageConfigLister: configlistersv1.NewImageLister(indexer),
	}

	result, err := observeInternalRegistryHostname(listers, map[string]interface{}{})
	if err != nil {
		t.Error("expected err == nil")
	}
	internalRegistryHostname, _, err := unstructured.NestedString(result, "imagePolicyConfig", "internalRegistryHostname")
	if err != nil {
		t.Fatal(err)
	}
	if internalRegistryHostname != expectedInternalRegistryHostname {
		t.Errorf("expected internal registry hostname: %s, got %s", expectedInternalRegistryHostname, internalRegistryHostname)
	}
}
