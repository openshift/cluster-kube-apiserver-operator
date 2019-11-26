package etcd

import (
	"fmt"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"reflect"
	"testing"
)

const clusterFQDN = "foo.bar"

func fakeObjectReference(ep *v1.Endpoints) *v1.ObjectReference {
	return &v1.ObjectReference{
		Kind:            ep.Kind,
		Namespace:       ep.Namespace,
		Name:            ep.Name,
		UID:             ep.UID,
		APIVersion:      ep.APIVersion,
		ResourceVersion: ep.ResourceVersion,
	}
}

func getWantObserverConfig(etcdURLs []string) (map[string]interface{}, error) {
	wantObserverConfig := map[string]interface{}{}
	if len(etcdURLs) == 0 {
		return wantObserverConfig, nil
	}
	storageConfigURLsPath := []string{"storageConfig", "urls"}
	if err := unstructured.SetNestedStringSlice(wantObserverConfig, etcdURLs, storageConfigURLsPath...); err != nil {
		return nil, err
	}
	return wantObserverConfig, nil
}

func getEndpoint(hostname, ip string) *v1.Endpoints {
	return &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "host-etcd",
			Namespace: "openshift-etcd",
			Annotations: map[string]string{
				"alpha.installer.openshift.io/dns-suffix": clusterFQDN,
			},
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       ip,
						Hostname: hostname,
					},
				},
			},
		},
	}
}

func TestObserveStorageURLs(t *testing.T) {
	tests := []struct {
		name            string
		indexer         cache.Indexer
		currentConfig   map[string]interface{}
		wantStorageURLs []string
		wantErrs        []error
		endpoint        *v1.Endpoints
	}{
		{
			name:            "test etcd-bootstrap with dummy IP",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{"https://etcd-bootstrap." + clusterFQDN + ":2379"},
			wantErrs:        nil,
			endpoint:        getEndpoint("etcd-bootstrap", "192.0.2.1"),
		},
		{
			name:            "test etcd-bootstrap with real IP",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{"https://10.0.0.1:2379"},
			wantErrs:        nil,
			endpoint:        getEndpoint("etcd-bootstrap", "10.0.0.1"),
		},
		{
			name:            "test etcd-bootstrap with invalid IPv4",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{},
			wantErrs: []error{fmt.Errorf("endpoints %s/%s: subsets[%v]addresses[%v].IP is not a valid IP address", etcdEndpointName, etcdEndpointNamespace, 0, 0),
				fmt.Errorf("endpoints %s/%s: no etcd endpoint addresses found", etcdEndpointNamespace, etcdEndpointName)},
			endpoint: getEndpoint("etcd-bootstrap", "192.192.0.2.1"),
		},
		{
			name:            "test etcd-bootstrap with valid IPv6",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{"https://FE80:CD00:0000:0CDE:1257:0000:211E:729C:2379"},
			wantErrs:        nil,
			endpoint:        getEndpoint("etcd-bootstrap", "FE80:CD00:0000:0CDE:1257:0000:211E:729C"),
		},
		{
			name:            "test etcd-bootstrap with invalid IPv6",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{},
			wantErrs: []error{
				fmt.Errorf("endpoints %s/%s: subsets[%v]addresses[%v].IP is not a valid IP address", etcdEndpointName, etcdEndpointNamespace, 0, 0),
				fmt.Errorf("endpoints %s/%s: no etcd endpoint addresses found", etcdEndpointNamespace, etcdEndpointName),
			},
			endpoint: getEndpoint("etcd-bootstrap", "FE80:CD00:0000:0CDE:1257:0000:211E:729C:invalid"),
		},
		{
			name:            "test etcd member",
			indexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
			currentConfig:   nil,
			wantStorageURLs: []string{"https://etcd-0." + clusterFQDN + ":2379"},
			wantErrs:        nil,
			endpoint:        getEndpoint("etcd-0", "192.0.2.1"),
		},
		//	TODO: Add more test cases
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			lister := configobservation.Listers{
				OpenshiftEtcdEndpointsLister: corev1listers.NewEndpointsLister(tt.indexer),
			}
			r := events.NewRecorder(client.CoreV1().Events("openshift-etcd"), "test-operator",
				fakeObjectReference(tt.endpoint))
			if err := tt.indexer.Add(tt.endpoint); err != nil {
				t.Errorf("error adding endpoint to store: %#v", err)
			}
			wantObserverConfig, err := getWantObserverConfig(tt.wantStorageURLs)
			if err != nil {
				t.Errorf("error getting wantObserverConfig: %#v", err)
			}
			gotObservedConfig, gotErrs := ObserveStorageURLs(lister, r, tt.currentConfig)
			if !reflect.DeepEqual(gotObservedConfig, wantObserverConfig) {
				t.Errorf("ObserveStorageURLs() gotObservedConfig = %v, want %v", gotObservedConfig, wantObserverConfig)
			}
			if !reflect.DeepEqual(gotErrs, tt.wantErrs) {
				t.Errorf("ObserveStorageURLs() gotErrs = %v, want %v", gotErrs, tt.wantErrs)
			}
		})
	}
}
