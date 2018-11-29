package network

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestObserveClusterConfig(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if err := indexer.Add(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: podCIDR \n  serviceCIDR: serviceCIDR\n",
		},
	}); err != nil {
		t.Fatal(err.Error())
	}
	listers := configobservation.Listers{
		ConfigmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errors := ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	if len(errors) > 0 {
		t.Error("expected len(errors) == 0")
	}
	restrictedCIDRs, _, err := unstructured.NestedSlice(result, "admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedCIDRs[0] != "podCIDR" {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != "serviceCIDR" {
		t.Error(restrictedCIDRs[1])
	}
}
