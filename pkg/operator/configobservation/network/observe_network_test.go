package network

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveRestrictedCIDRs(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	listers := configobservation.Listers{
		NetworkLister: configlistersv1.NewNetworkLister(indexer),
	}

	// With no network configured, check that a rump configuration is returned
	result, errors := ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	if len(errors) > 0 {
		t.Error("expected len(errors) == 0")
	}
	if result == nil {
		t.Errorf("expected result != nil")
	}

	conf, ok, err := unstructured.NestedMap(result, "admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf["kind"] != "RestrictedEndpointsAdmissionConfig" {
		t.Errorf("unexpected Kind %v", conf["kind"])
	}
	if conf["apiVersion"] != "network.openshift.io/v1" {
		t.Errorf("unexpected APIVersion %v", conf["apiVersion"])
	}

	cidrs, ok, err := unstructured.NestedStringSlice(result, "admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if len(cidrs) != 0 {
		t.Errorf("expected restrictedCIDRs to be empty, got %v", cidrs)
	}

	// Next, add the network config and see that it reacts
	if err := indexer.Add(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "podCIDR"}},
			ServiceNetwork: []string{"serviceCIDR"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})

	restrictedCIDRs, _, err := unstructured.NestedStringSlice(result, "admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedCIDRs[0] != "podCIDR" {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != "serviceCIDR" {
		t.Error(restrictedCIDRs[1])
	}

	// Update the network config and see that it works
	if err := indexer.Update(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "podCIDR2"}},
			ServiceNetwork: []string{"serviceCIDR2"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	// Note that we pass the previous result back in
	result, errors = ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), result)

	restrictedCIDRs, _, err = unstructured.NestedStringSlice(result, "admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedCIDRs[0] != "podCIDR2" {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != "serviceCIDR2" {
		t.Error(restrictedCIDRs[1])
	}

	// When the network object goes missing (simulate transient failure),
	// you stll get the old config
	if err := indexer.Delete(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), result)

	restrictedCIDRs, _, err = unstructured.NestedStringSlice(result, "admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	if err != nil {
		t.Fatal(err)
	}
	if len(restrictedCIDRs) != 2 {
		t.Fatalf("expected 2 restrictedCIDRs, got %v", result)
	}
	if restrictedCIDRs[0] != "podCIDR2" {
		t.Error(restrictedCIDRs[0])
	}
	if restrictedCIDRs[1] != "serviceCIDR2" {
		t.Error(restrictedCIDRs[1])
	}

}

func TestObserveServicesSubnet(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	listers := configobservation.Listers{
		NetworkLister: configlistersv1.NewNetworkLister(indexer),
	}

	// With no network configured, check that a rump configuration is returned
	result, errors := ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	if len(errors) > 0 {
		t.Error("expected len(errors) == 0")
	}
	if result == nil {
		t.Errorf("expected result != nil")
	}

	conf, ok, err := unstructured.NestedString(result, "servicesSubnet")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "" {
		t.Errorf("Unexpected value: %v", conf)
	}

	// Next, add the network config and see that it reacts
	if err := indexer.Add(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "podCIDR"}},
			ServiceNetwork: []string{"serviceCIDR"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	conf, ok, err = unstructured.NestedString(result, "servicesSubnet")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "serviceCIDR" {
		t.Errorf("Unexpected value: %v", conf)
	}

	// Change the config and see that it is updated.
	if err := indexer.Update(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "podCIDR1"}},
			ServiceNetwork: []string{"serviceCIDR1"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), result)
	conf, ok, err = unstructured.NestedString(result, "servicesSubnet")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "serviceCIDR1" {
		t.Errorf("Unexpected value: %v", conf)
	}
}
