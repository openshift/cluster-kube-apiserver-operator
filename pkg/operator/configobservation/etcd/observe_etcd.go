package etcd

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

const etcdNamespaceName = "kube-system"

// ObserveStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func ObserveStorageURLs(genericListers configobserver.Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	listers := genericListers.(configobservation.Listers)
	observedConfig = map[string]interface{}{}
	storageConfigURLsPath := []string{"storageConfig", "urls"}
	if currentEtcdURLs, found, _ := unstructured.NestedStringSlice(currentConfig, storageConfigURLsPath...); found {
		errs = append(errs, unstructured.SetNestedStringSlice(observedConfig, currentEtcdURLs, storageConfigURLsPath...))
	}

	var etcdURLs []string
	etcdEndpoints, err := listers.EndpointsLister.Endpoints(etcdNamespaceName).Get("host-etcd")
	if errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("endpoints/host-etcd.kube-system: not found"))
		return
	}
	if err != nil {
		errs = append(errs, err)
		return
	}
	dnsSuffix := etcdEndpoints.Annotations["alpha.installer.openshift.io/dns-suffix"]
	if len(dnsSuffix) == 0 {
		errs = append(errs, fmt.Errorf("endpoints/host-etcd.kube-system: alpha.installer.openshift.io/dns-suffix annotation not found"))
		return
	}
	for subsetIndex, subset := range etcdEndpoints.Subsets {
		for addressIndex, address := range subset.Addresses {
			if address.Hostname == "" {
				errs = append(errs, fmt.Errorf("endpoints/host-etcd.kube-system: subsets[%v]addresses[%v].hostname not found", subsetIndex, addressIndex))
				continue
			}
			etcdURLs = append(etcdURLs, "https://"+address.Hostname+"."+dnsSuffix+":2379")
		}
	}

	if len(etcdURLs) == 0 {
		errs = append(errs, fmt.Errorf("endpoints/host-etcd.kube-system: no etcd endpoint addresses found"))
	}
	if len(errs) > 0 {
		return
	}

	errs = append(errs, unstructured.SetNestedStringSlice(observedConfig, etcdURLs, storageConfigURLsPath...))
	return
}
