package etcdendpoints

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

func TestObserveStorageURLs(t *testing.T) {
	tests := []struct {
		name          string
		currentConfig map[string]interface{}
		fallback      configobserver.ObserveConfigFunc
		expected      map[string]interface{}
		expectErrors  bool
		endpoint      *v1.ConfigMap
	}{
		{
			name:          "ValidIPv4",
			currentConfig: observedConfig(withStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("10.0.0.1")),
			expected:      observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "ValidIPv4WithOldStorageURL",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("10.0.0.1")),
			expected:      observedConfig(withStorageURL("https://10.0.0.1:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "ValidIPv6",
			currentConfig: observedConfig(withStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C")),
			expected:      observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
		},
		{
			name:          "ValidIPv6WithOldStorageURL",
			currentConfig: observedConfig(withOldStorageURL("https://previous.url:2379")),
			endpoint:      endpoints(withAddress("FE80:CD00:0000:0CDE:1257:0000:211E:729C")),
			expected:      observedConfig(withStorageURL("https://[fe80:cd00:0:cde:1257:0:211e:729c]:2379"), withLocalhostStorageURLs()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			lister := configobservation.Listers{
				ConfigmapLister_: corev1listers.NewConfigMapLister(indexer),
			}
			if tt.endpoint != nil {
				if err := indexer.Add(tt.endpoint); err != nil {
					t.Fatalf("error adding endpoint to store: %#v", err)
				}
			}
			actual, errs := ObserveStorageURLs(lister, events.NewInMemoryRecorder("test", clock.RealClock{}), tt.currentConfig)
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

func endpoints(configs ...func(endpoints *v1.ConfigMap)) *v1.ConfigMap {
	endpoints := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-endpoints",
			Namespace: "openshift-etcd",
		},
		Data: map[string]string{},
	}
	for _, config := range configs {
		config(endpoints)
	}
	return endpoints
}

func withBootstrap(ip string) func(*v1.ConfigMap) {
	return func(endpoints *v1.ConfigMap) {
		if endpoints.Annotations == nil {
			endpoints.Annotations = map[string]string{}
		}
		endpoints.Annotations["alpha.installer.openshift.io/etcd-bootstrap"] = ip
	}
}

func withAddress(ip string) func(*v1.ConfigMap) {
	return func(endpoints *v1.ConfigMap) {
		endpoints.Data[base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(ip))] = ip
	}
}

func fallback(observed map[string]interface{}, errs ...error) configobserver.ObserveConfigFunc {
	return func(genericListers configobserver.Listers, recorder events.Recorder, currentConfig map[string]interface{}) (map[string]interface{}, []error) {
		return observed, errs
	}
}
