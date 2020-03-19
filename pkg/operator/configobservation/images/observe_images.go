package images

import (
	"bytes"
	"encoding/json"

	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

// ObserveInternalRegistryHostname reads the internal registry hostname from the cluster configuration as provided by
// the registry operator.
func ObserveInternalRegistryHostname(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	internalRegistryHostnamePath := []string{"imagePolicyConfig", "internalRegistryHostname"}
	defer func() {
		ret = configobserver.Pruned(ret, internalRegistryHostnamePath)
	}()

	listers := genericListers.(configobservation.Listers)
	errs := []error{}

	observedConfig := map[string]interface{}{}
	configImage, err := listers.ImageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		klog.Warningf("image.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return existingConfig, append(errs, err)
	}

	internalRegistryHostName := configImage.Status.InternalRegistryHostname
	if len(internalRegistryHostName) > 0 {
		if err := unstructured.SetNestedField(observedConfig, internalRegistryHostName, internalRegistryHostnamePath...); err != nil {
			errs = append(errs, err)
		}
		currentInternalRegistryHostname, _, err := unstructured.NestedString(existingConfig, internalRegistryHostnamePath...)
		if err != nil {
			errs = append(errs, err)
			// keep going on read error from existing config
		}
		if internalRegistryHostName != currentInternalRegistryHostname {
			recorder.Eventf("ObserveInternalRegistryHostnameChanged", "Internal registry hostname changed to %q", internalRegistryHostName)
		}
	}
	return observedConfig, errs
}

// ObserveExternalRegistryHostnames maps the user provided+generated external registry hostnames to the kube api server
// configuration.
func ObserveExternalRegistryHostnames(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	externalRegistryHostnamePath := []string{"imagePolicyConfig", "externalRegistryHostnames"}
	defer func() {
		ret = configobserver.Pruned(ret, externalRegistryHostnamePath)
	}()

	listers := genericListers.(configobservation.Listers)
	var errs []error

	// now gather the cluster config and turn it into the observed config
	observedConfig := map[string]interface{}{}
	configImage, err := listers.ImageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		klog.Warningf("image.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return existingConfig, append(errs, err)
	}

	// User provided values take precedence, first entry in the array
	// has special significance.
	externalRegistryHostnames := configImage.Spec.ExternalRegistryHostnames
	externalRegistryHostnames = append(externalRegistryHostnames, configImage.Status.ExternalRegistryHostnames...)

	if len(externalRegistryHostnames) > 0 {
		if err = unstructured.SetNestedStringSlice(observedConfig, externalRegistryHostnames, externalRegistryHostnamePath...); err != nil {
			return existingConfig, append(errs, err)
		}
	}

	// first observe all the existing config values so that if we get any errors
	// we can at least return those.
	existingHostnames, _, err := unstructured.NestedStringSlice(existingConfig, externalRegistryHostnamePath...)
	if err != nil {
		errs = append(errs, err)
		// keep going on read error from existing config
	}
	if !equality.Semantic.DeepEqual(existingHostnames, externalRegistryHostnames) {
		recorder.Eventf("ObserveExternalRegistryHostnameChanged", "External registry hostname changed to %v", externalRegistryHostnames)
	}

	return observedConfig, errs
}

// ObserveAllowedRegistriesForImport maps the user provided list of allowed registries for importing images to the kube api server
// configuration.
func ObserveAllowedRegistriesForImport(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	var errs []error
	prevObservedConfig := map[string]interface{}{}

	// first observe all the existing config values so that if we get any errors
	// we can at least return those.
	allowedRegistriesForImportPath := []string{"imagePolicyConfig", "allowedRegistriesForImport"}
	existingAllowedRegistries, _, err := unstructured.NestedSlice(existingConfig, allowedRegistriesForImportPath...)
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}
	if len(existingAllowedRegistries) > 0 {
		err := unstructured.SetNestedSlice(prevObservedConfig, existingAllowedRegistries, allowedRegistriesForImportPath...)
		if err != nil {
			return prevObservedConfig, append(errs, err)
		}
	}

	// now gather the cluster config and turn it into the observed config
	observedConfig := map[string]interface{}{}
	configImage, err := listers.ImageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		klog.Warningf("image.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}

	if len(configImage.Spec.AllowedRegistriesForImport) > 0 {
		allowed, err := convert(configImage.Spec.AllowedRegistriesForImport)
		if err != nil {
			return prevObservedConfig, append(errs, err)
		}
		err = unstructured.SetNestedField(observedConfig, allowed, allowedRegistriesForImportPath...)
		if err != nil {
			return prevObservedConfig, append(errs, err)
		}
	}

	newAllowedRegistries, _, err := unstructured.NestedSlice(observedConfig, allowedRegistriesForImportPath...)
	if err != nil || !equality.Semantic.DeepEqual(existingAllowedRegistries, newAllowedRegistries) {
		recorder.Eventf("ObserveAllowedRegistriesForImport", "Allowed registries for import changed to %v", configImage.Spec.AllowedRegistriesForImport)
	}

	return observedConfig, errs
}

// convert converts an arbitrary object into the json decoded equivalent by
// first encoding it into a json string and then decoding the string and
// returning it.
func convert(o interface{}) (interface{}, error) {
	if o == nil {
		return nil, nil
	}
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(o); err != nil {
		return nil, err
	}

	ret := []interface{}{}
	if err := json.NewDecoder(buf).Decode(&ret); err != nil {
		return nil, err
	}

	return ret, nil
}
