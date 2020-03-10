package etcd

import (
	"fmt"
	"net"
	"reflect"
	"strings"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

const (
	etcdEndpointNamespace = "openshift-etcd"
	etcdEndpointName      = "host-etcd-2"
)

// ObserveStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func ObserveStorageURLs(genericListers configobserver.Listers, recorder events.Recorder, currentConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	storageConfigURLsPath := []string{"storageConfig", "urls"}
	var errs []error

	previouslyObservedConfig := map[string]interface{}{}
	currentEtcdURLs, found, err := unstructured.NestedStringSlice(currentConfig, storageConfigURLsPath...)
	if err != nil {
		errs = append(errs, err)
	}
	if found {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, currentEtcdURLs, storageConfigURLsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	var etcdURLs []string
	etcdEndpoints, err := listers.OpenshiftEtcdEndpointsLister.Endpoints(etcdEndpointNamespace).Get(etcdEndpointName)
	if errors.IsNotFound(err) {
		recorder.Warningf("ObserveStorageFailed", "Required %s/%s endpoint not found", etcdEndpointNamespace, etcdEndpointName)
		return previouslyObservedConfig, append(errs, fmt.Errorf("endpoints/%s.%s: not found", etcdEndpointName, etcdEndpointNamespace))
	}
	if err != nil {
		recorder.Warningf("ObserveStorageFailed", "Error getting %s/%s endpoint: %v", etcdEndpointNamespace, etcdEndpointName, err)
		return previouslyObservedConfig, append(errs, err)
	}

	// note: etcd bootstrap should never be added to the in-cluster kube-apiserver
	// this can result in some early pods crashlooping, but ensures that we never contact the bootstrap machine from
	// the in-cluster kube-apiserver so we can safely teardown out of order.

	for subsetIndex, subset := range etcdEndpoints.Subsets {
		for addressIndex, address := range subset.Addresses {
			ip := net.ParseIP(address.IP)
			if ip == nil {
				ipErr := fmt.Errorf("endpoints/%s in the %s namespace: subsets[%v]addresses[%v].IP is not a valid IP address", etcdEndpointName, etcdEndpointNamespace, subsetIndex, addressIndex)
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
	}

	if len(etcdURLs) == 0 {
		emptyURLErr := fmt.Errorf("endpoints %s/%s: no etcd endpoint addresses found", etcdEndpointNamespace, etcdEndpointName)
		recorder.Warning("ObserveStorageFailed", emptyURLErr.Error())
		errs = append(errs, emptyURLErr)
	}

	// always append `localhost` url
	etcdURLs = append(etcdURLs, "https://localhost:2379")

	observedConfig := map[string]interface{}{}
	if err := unstructured.SetNestedStringSlice(observedConfig, etcdURLs, storageConfigURLsPath...); err != nil {
		return previouslyObservedConfig, append(errs, err)
	}

	if !reflect.DeepEqual(currentEtcdURLs, etcdURLs) {
		recorder.Eventf("ObserveStorageUpdated", "Updated storage urls to %s", strings.Join(etcdURLs, ","))
	}

	return observedConfig, errs
}
