package etcdendpoints

import (
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

const (
	etcdEndpointNamespace = "openshift-etcd"
	etcdEndpointName      = "etcd-endpoints"
)

// ObserveStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func ObserveStorageURLs(genericListers configobserver.Listers, recorder events.Recorder, currentConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	oldStorageConfigURLsPath := []string{"storageConfig", "urls"}
	newStorageConfigURLsPath := []string{"apiServerArguments", "etcd-servers"}
	var errs []error

	previouslyObservedConfig := map[string]interface{}{}
	oldCurrentEtcdURLs, oldFound, err := unstructured.NestedStringSlice(currentConfig, oldStorageConfigURLsPath...)
	if err != nil {
		errs = append(errs, err)
	}
	newCurrentEtcdURLs, newFound, err := unstructured.NestedStringSlice(currentConfig, newStorageConfigURLsPath...)
	if err != nil {
		errs = append(errs, err)
	}
	var currentEtcdURLs []string
	if newFound {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, newCurrentEtcdURLs, newStorageConfigURLsPath...); err != nil {
			errs = append(errs, err)
		}
		currentEtcdURLs = newCurrentEtcdURLs
	} else if oldFound {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, oldCurrentEtcdURLs, newStorageConfigURLsPath...); err != nil {
			errs = append(errs, err)
		}
		currentEtcdURLs = oldCurrentEtcdURLs
	}

	var etcdURLs []string
	etcdEndpoints, err := listers.ConfigmapLister.ConfigMaps(etcdEndpointNamespace).Get(etcdEndpointName)
	if err != nil {
		recorder.Warningf("ObserveStorageFailed", "Error getting %s/%s configmap: %v", etcdEndpointNamespace, etcdEndpointName, err)
		return previouslyObservedConfig, append(errs, err)
	}

	// note: etcd bootstrap should never be added to the in-cluster kube-apiserver
	// this can result in some early pods crashlooping, but ensures that we never contact the bootstrap machine from
	// the in-cluster kube-apiserver so we can safely teardown out of order.

	for k := range etcdEndpoints.Data {
		address := etcdEndpoints.Data[k]
		ip := net.ParseIP(address)
		if ip == nil {
			ipErr := fmt.Errorf("configmaps/%s in the %s namespace: %v is not a valid IP address", etcdEndpointName, etcdEndpointNamespace, address)
			errs = append(errs, ipErr)
			continue
		}
		// skip placeholder ip addresses used in previous versions where the hostname was used instead
		if strings.HasPrefix(ip.String(), "192.0.2.") || strings.HasPrefix(ip.String(), "2001:db8:") {
			// not considered an error
			continue
		}
		// use the canonical representation of the ip address (not original input) when constructing the url
		if ip.To4() != nil {
			etcdURLs = append(etcdURLs, fmt.Sprintf("https://%s:2379", ip))
		} else {
			etcdURLs = append(etcdURLs, fmt.Sprintf("https://[%s]:2379", ip))
		}
	}

	if len(etcdURLs) == 0 {
		emptyURLErr := fmt.Errorf("configmaps %s/%s: no etcd endpoint addresses found", etcdEndpointNamespace, etcdEndpointName)
		recorder.Warning("ObserveStorageFailed", emptyURLErr.Error())
		errs = append(errs, emptyURLErr)
	}

	// always append `localhost` url
	etcdURLs = append(etcdURLs, "https://localhost:2379")

	sort.Strings(etcdURLs)

	observedConfig := map[string]interface{}{}
	if err := unstructured.SetNestedStringSlice(observedConfig, etcdURLs, newStorageConfigURLsPath...); err != nil {
		return previouslyObservedConfig, append(errs, err)
	}

	if !reflect.DeepEqual(currentEtcdURLs, etcdURLs) {
		recorder.Eventf("ObserveStorageUpdated", "Updated storage urls to %s", strings.Join(etcdURLs, ","))
	}

	return observedConfig, errs
}
