package network

import (
	"testing"

	"github.com/ghodss/yaml"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
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
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/RestrictedEndpointsAdmission:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: RestrictedEndpointsAdmissionConfig
`)

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
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/RestrictedEndpointsAdmission:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: RestrictedEndpointsAdmissionConfig
        restrictedCIDRs:
        - podCIDR
        - serviceCIDR
`)

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

	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/RestrictedEndpointsAdmission:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: RestrictedEndpointsAdmissionConfig
        restrictedCIDRs:
        - podCIDR2
        - serviceCIDR2
`)

	// When the network object goes missing (simulate transient failure),
	// you stll get the old config
	if err := indexer.Delete(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveRestrictedCIDRs(listers, events.NewInMemoryRecorder("network"), result)

	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/RestrictedEndpointsAdmission:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: RestrictedEndpointsAdmissionConfig
        restrictedCIDRs:
        - podCIDR2
        - serviceCIDR2
`)
}

func TestObserveServicesSubnet(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	listers := configobservation.Listers{
		NetworkLister: configlistersv1.NewNetworkLister(indexer),
	}

	// With no network configured, check that a rump configuration is returned
	result, errors := ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	if len(errors) > 0 {
		t.Errorf("expected len(errors) == 0: %v", errors)
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
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	if len(errors) > 0 {
		t.Errorf("expected len(errors) == 0: %v", errors)
	}
	conf, ok, err = unstructured.NestedString(result, "servicesSubnet")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "172.30.0.0/16" {
		t.Errorf("Unexpected value: %v", conf)
	}
	conf, ok, err = unstructured.NestedString(result, "servingInfo", "bindAddress")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "0.0.0.0:6443" {
		t.Errorf("Unexpected value: %v", conf)
	}
	conf, ok, err = unstructured.NestedString(result, "servingInfo", "bindNetwork")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "tcp4" {
		t.Errorf("Unexpected value: %v", conf)
	}

	// Change the config and see that it is updated.
	if err := indexer.Update(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
			ServiceNetwork: []string{"fd02::/112"},
		},
	}); err != nil {
		t.Fatal(err.Error())
	}

	result, errors = ObserveServicesSubnet(listers, events.NewInMemoryRecorder("network"), result)
	if len(errors) > 0 {
		t.Errorf("expected len(errors) == 0: %v", errors)
	}
	conf, ok, err = unstructured.NestedString(result, "servicesSubnet")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "fd02::/112" {
		t.Errorf("Unexpected value: %v", conf)
	}
	conf, ok, err = unstructured.NestedString(result, "servingInfo", "bindAddress")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "[::]:6443" {
		t.Errorf("Unexpected value: %v", conf)
	}
	conf, ok, err = unstructured.NestedString(result, "servingInfo", "bindNetwork")
	if err != nil || !ok {
		t.Errorf("Unexpected configuration returned: %v", result)
	}
	if conf != "tcp6" {
		t.Errorf("Unexpected value: %v", conf)
	}
}

func TestObserveExternalIPPolicy(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	listers := configobservation.Listers{
		NetworkLister: configlistersv1.NewNetworkLister(indexer),
	}

	// No configuration -> default deny
	result, errors := ObserveExternalIPPolicy(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/ExternalIPRanger:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: ExternalIPRangerAdmissionConfig
        allowIngressIP: false
        apiVersion: network.openshift.io/v1`)

	// configuration with no policy -> default deny
	err := indexer.Add(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       configv1.NetworkSpec{},
	})
	assert.Nil(t, err)

	result, errors = ObserveExternalIPPolicy(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/ExternalIPRanger:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: ExternalIPRangerAdmissionConfig
        allowIngressIP: false
        apiVersion: network.openshift.io/v1`)

	// configuration with empty policy -> deny
	err = indexer.Update(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.NetworkSpec{
			ExternalIP: &configv1.ExternalIPConfig{
				Policy: &configv1.ExternalIPPolicy{},
			},
		},
	})
	assert.Nil(t, err)

	result, errors = ObserveExternalIPPolicy(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/ExternalIPRanger:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: ExternalIPRangerAdmissionConfig
        allowIngressIP: false
        apiVersion: network.openshift.io/v1`)

	// non-empty policy -> generated correctly
	err = indexer.Update(&configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.NetworkSpec{
			ExternalIP: &configv1.ExternalIPConfig{
				Policy: &configv1.ExternalIPPolicy{
					RejectedCIDRs: []string{"1.0.0.0/20", "2.0.2.0/24"},
					AllowedCIDRs:  []string{"3.0.0.0/18", "42.0.1.0/30"},
				},
				AutoAssignCIDRs: []string{"99.1.0.0/24"},
			},
		},
	})
	assert.Nil(t, err)

	result, errors = ObserveExternalIPPolicy(listers, events.NewInMemoryRecorder("network"), map[string]interface{}{})
	assert.Empty(t, errors)
	shouldMatchYaml(t, result, `
admission:
  pluginConfig:
    network.openshift.io/ExternalIPRanger:
      configuration:
        apiVersion: network.openshift.io/v1
        kind: ExternalIPRangerAdmissionConfig
        allowIngressIP: true
        apiVersion: network.openshift.io/v1
        externalIPNetworkCIDRs:
        - "!1.0.0.0/20"
        - "!2.0.2.0/24"
        - 3.0.0.0/18
        - 42.0.1.0/30
`)
}

func shouldMatchYaml(t *testing.T, obj map[string]interface{}, expected string) {
	t.Helper()
	exp := map[string]interface{}{}
	err := yaml.Unmarshal([]byte(expected), &exp)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, exp, obj)
}
