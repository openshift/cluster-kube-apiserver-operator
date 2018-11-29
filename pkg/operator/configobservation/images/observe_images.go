package images

import (
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

// ObserveInternalRegistryHostname reads the internal registry hostname from the cluster configuration as provided by
// the registry operator.
func ObserveInternalRegistryHostname(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	internalRegistryHostnamePath := []string{"imagePolicyConfig", "internalRegistryHostname"}
	currentInternalRegistryHostname, _, err := unstructured.NestedString(existingConfig, internalRegistryHostnamePath...)
	if err != nil {
		errs = append(errs, err)
	}
	if len(currentInternalRegistryHostname) > 0 {
		if err := unstructured.SetNestedField(prevObservedConfig, currentInternalRegistryHostname, internalRegistryHostnamePath...); err != nil {
			errs = append(errs, err)
		}
	}

	if !listers.ImageConfigSynced() {
		glog.Warning("images.config.openshift.io not synced")
		return prevObservedConfig, errs
	}

	observedConfig := map[string]interface{}{}
	configImage, err := listers.ImageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		glog.Warningf("image.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return prevObservedConfig, errs
	}

	internalRegistryHostName := configImage.Status.InternalRegistryHostname
	if len(internalRegistryHostName) > 0 {
		if err := unstructured.SetNestedField(observedConfig, internalRegistryHostName, internalRegistryHostnamePath...); err != nil {
			errs = append(errs, err)
		}
		if internalRegistryHostName != currentInternalRegistryHostname {
			recorder.Eventf("ObserveRegistryHostnameChanged", "Internal registry hostname changed to %q", internalRegistryHostName)
		}
	}
	return observedConfig, errs
}
