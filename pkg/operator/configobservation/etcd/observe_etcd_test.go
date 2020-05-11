package etcd

import (
	"reflect"
	"testing"

	"github.com/openshift/library-go/pkg/operator/events"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

const clusterFQDN = "foo.bar"

func TestObserveStorageURLs(t *testing.T) {
	tests := []struct {
		name          string
		currentConfig map[string]interface{}
		expected      map[string]interface{}
		expectErrors  bool
		endpoint      *v1.Endpoints
	}{
		{
			name:          "NoEtcdHosts",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			expected:      observedConfig(withStorageURL("https://previous.url:2379")),
			expectErrors:  true,
		},
		{
			name:          "ValidIPv4",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("10.0.0.1")),
			expected:      observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "ValidIPv4New",
			currentConfig: observedConfig(withStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("10.0.0.1")),
			expected:      observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "InvalidIPv4",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("10.0.0.1"),
				withAddress("192.192.0.2.1"),
			),
			expected:     observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
			expectErrors: true,
		},
		{
			name:          "ValidIPv6",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C")),
			expected:      observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "InvalidIPv6",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C"),
				withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C:invalid"),
			),
			expected:     observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
			expectErrors: true,
		},
		{
			name:          "FakeIPv4",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("10.0.0.1"),
				withAddress("192.0.2.1"),
			),
			expected: observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "FakeIPv6",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C"),
				withAddress("2001:0DB8:0000:0CDE:1257:0000:211E:729C"),
			),
			expected: observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "ValidIPv4AsIPv6Literal",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("::ffff:a00:1")),
			expected:      observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "FakeIPv4AsIPv6Literal",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C"),
				withAddress("::ffff:c000:201"),
			),
			expected: observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "NoAddressesFound",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(),
			expected:      observedConfig(withLocalhostStorageURLs()),
			expectErrors:  true,
		},
		{
			name:          "OnlyFakeAddressesFound",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withAddress("192.0.2.1"),
				withAddress("::ffff:c000:201"),
			),
			expected:     observedConfig(withLocalhostStorageURLs()),
			expectErrors: true,
		},
		{
			name:          "IgnoreBootstrap",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint: endpoints(
				withBootstrap("10.0.0.2"),
				withAddress("10.0.0.1"),
			),
			expected: observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			lister := configobservation.Listers{
				OpenshiftEtcdEndpointsLister: corev1listers.NewEndpointsLister(indexer),
			}
			if tt.endpoint != nil {
				if err := indexer.Add(tt.endpoint); err != nil {
					t.Fatalf("error adding endpoint to store: %#v", err)
				}
			}
			actual, errs := ObserveStorageURLs(lister, events.NewInMemoryRecorder("test"), tt.currentConfig)
			if tt.expectErrors && len(errs) == 0 {
				t.Errorf("errors expected")
			}
			if !tt.expectErrors && len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("ObserveStorageURLs() gotObservedConfig = %v, want %v", actual, tt.expected)
			}
			if t.Failed() {
				t.Log("\n" + mergepatch.ToYAMLOrError(actual))
				for _, err := range errs {
					t.Log(err)
				}
			}
		})
	}
}

func observedConfig(configs ...func(map[string]interface{})) map[string]interface{} {
	observedConfig := map[string]interface{}{}
	for _, config := range configs {
		config(observedConfig)
	}
	return observedConfig
}

func withOldStorageURL(url string) func(map[string]interface{}) {
	return func(observedConfig map[string]interface{}) {
		urls, _, _ := unstructured.NestedStringSlice(observedConfig, "storageConfig", "urls")
		urls = append(urls, url)
		_ = unstructured.SetNestedStringSlice(observedConfig, urls, "storageConfig", "urls")
	}
}

func withStorageURL(url string) func(map[string]interface{}) {
	return func(observedConfig map[string]interface{}) {
		urls, _, _ := unstructured.NestedStringSlice(observedConfig, "apiServerArguments", "etcd-servers")
		urls = append(urls, url)
		_ = unstructured.SetNestedStringSlice(observedConfig, urls, "apiServerArguments", "etcd-servers")
	}
}

func withOldLocalhostStorageURLs() func(map[string]interface{}) {
	return func(observedConfig map[string]interface{}) {
		urls, _, _ := unstructured.NestedStringSlice(observedConfig, "storageConfig", "urls")
		urls = append(urls, "https://localhost:2379")
		_ = unstructured.SetNestedStringSlice(observedConfig, urls, "storageConfig", "urls")
	}
}

func withLocalhostStorageURLs() func(map[string]interface{}) {
	return func(observedConfig map[string]interface{}) {
		urls, _, _ := unstructured.NestedStringSlice(observedConfig, "apiServerArguments", "etcd-servers")
		urls = append(urls, "https://localhost:2379")
		_ = unstructured.SetNestedStringSlice(observedConfig, urls, "apiServerArguments", "etcd-servers")
	}
}

func endpoints(configs ...func(endpoints *v1.Endpoints)) *v1.Endpoints {
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "host-etcd-2",
			Namespace: "openshift-etcd",
		},
		Subsets: []v1.EndpointSubset{{Addresses: []v1.EndpointAddress{}}},
	}
	for _, config := range configs {
		config(endpoints)
	}
	return endpoints
}

func withBootstrap(ip string) func(*v1.Endpoints) {
	return func(endpoints *v1.Endpoints) {
		if endpoints.Annotations == nil {
			endpoints.Annotations = map[string]string{}
		}
		endpoints.Annotations["alpha.installer.openshift.io/etcd-bootstrap"] = ip
	}
}

func withAddress(ip string) func(*v1.Endpoints) {
	return func(endpoints *v1.Endpoints) {
		endpoints.Subsets[0].Addresses = append(endpoints.Subsets[0].Addresses, v1.EndpointAddress{
			IP: ip,
		})
	}
}
