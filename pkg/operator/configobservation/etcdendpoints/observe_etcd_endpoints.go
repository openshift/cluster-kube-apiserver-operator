package etcdendpoints

import (
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	endpointsobserver "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/etcd"
)

const (
	etcdEndpointNamespace = "openshift-etcd"
	etcdEndpointName      = "etcd-endpoints"
)

var fallbackObserver configobserver.ObserveConfigFunc = endpointsobserver.ObserveStorageURLs

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
	etcdEndpoints, err := listers.ConfigmapLister.ConfigMaps(etcdEndpointNamespace).Get(etcdEndpointName)
	if errors.IsNotFound(err) {
		// In clusters prior to 4.5, fall back to reading the old host-etcd-2 endpoint
		// resource, if possible. In 4.6 we can assume consumers have been updated to
		// use the configmap, delete the fallback code, and throw an error if the
		// configmap doesn't exist.
		observedConfig, fallbackErrors := fallbackObserver(listers, recorder, currentConfig)
		if len(fallbackErrors) > 0 {
			errs = append(errs, fallbackErrors...)
			return previouslyObservedConfig, append(errs, fmt.Errorf("configmap %s/%s not found, and fallback observer failed", etcdEndpointNamespace, etcdEndpointName))
		}
		return observedConfig, errs
	}
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
	if err := unstructured.SetNestedStringSlice(observedConfig, etcdURLs, storageConfigURLsPath...); err != nil {
		return previouslyObservedConfig, append(errs, err)
	}

	if !reflect.DeepEqual(currentEtcdURLs, etcdURLs) {
		recorder.Eventf("ObserveStorageUpdated", "Updated storage urls to %s", strings.Join(etcdURLs, ","))
	}

	return observedConfig, errs
}
