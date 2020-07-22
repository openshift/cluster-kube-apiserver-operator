package etcdendpoints

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	libgoetcd "github.com/openshift/library-go/pkg/operator/configobserver/etcd"
	"github.com/openshift/library-go/pkg/operator/events"
)

// ObserveStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func ObserveStorageURLs(genericListers configobserver.Listers, recorder events.Recorder, currentConfig map[string]interface{}) (map[string]interface{}, []error) {
	var errs []error

	previouslyObservedConfig := map[string]interface{}{}
	oldCurrentEtcdURLs, oldFound, err := unstructured.NestedStringSlice(currentConfig, libgoetcd.OldStorageConfigURLsPath...)
	if err != nil {
		errs = append(errs, err)
	}
	newCurrentEtcdURLs, newFound, err := unstructured.NestedStringSlice(currentConfig, libgoetcd.StorageConfigURLsPath...)
	if err != nil {
		errs = append(errs, err)
	}
	if newFound {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, newCurrentEtcdURLs, libgoetcd.StorageConfigURLsPath...); err != nil {
			errs = append(errs, err)
		}
	} else if oldFound {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, oldCurrentEtcdURLs, libgoetcd.StorageConfigURLsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	updatedConfig, newErrs := libgoetcd.ObserveStorageURLsToArgumentsWithAlwaysLocal(genericListers, recorder, previouslyObservedConfig)
	return updatedConfig, append(errs, newErrs...)
}
