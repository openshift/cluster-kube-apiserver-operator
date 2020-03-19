package apiserver

import (
	"fmt"
	"strings"

	"github.com/imdario/mergo"
	"k8s.io/klog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

const (
	namedUserServingCertResourceNameFormat = "user-serving-cert-%03d"
)

var namedUserServingCertResourceNames = []string{
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 0),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 1),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 2),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 3),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 4),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 5),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 6),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 7),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 8),
	fmt.Sprintf(namedUserServingCertResourceNameFormat, 9),
}

var maxUserNamedCerts = len(namedUserServingCertResourceNames)

// syncActionRules rules define source resource names indexed by destination resource names.
// Empty value means to delete the destination.
type syncActionRules map[string]string

// resourceSyncFunc syncs a resource from the source location to the destination location.
type resourceSyncFunc func(destination, source resourcesynccontroller.ResourceLocation) error

// observeAPIServerConfigFunc observes configuration and returns the observedConfig and a map describing a list of
// resources to sync.
// It returns the observed config, sync rules and possibly an error. Nil sync rules mean to ignore all resources
// in case of error. Otherwise, resources are deleted by default and the returned sync rules are taken as overrides of that.
type observeAPIServerConfigFunc func(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, syncActionRules, []error)

// ObserveUserClientCABundle returns an ObserveConfigFunc that observes a user managed certificate bundle containing
// signers that will be recognized for incoming client certificates in addition to the operator managed signers.
var ObserveUserClientCABundle configobserver.ObserveConfigFunc = (&apiServerObserver{
	observerFunc:  observeUserClientCABundle,
	configPaths:   [][]string{},
	resourceNames: []string{"user-client-ca"},
	resourceType:  corev1.ConfigMap{},
}).observe

// ObserveNamedCertificates returns an ObserveConfigFunc that observes user managed TLS cert info for serving secure
// traffic to specific hostnames.
var ObserveNamedCertificates configobserver.ObserveConfigFunc = (&apiServerObserver{
	observerFunc:  observeNamedCertificates,
	configPaths:   [][]string{{"servingInfo", "namedCertificates"}},
	resourceNames: namedUserServingCertResourceNames,
	resourceType:  corev1.Secret{},
}).observe

// observeUserClientCABundle observes a user managed ConfigMap containing a certificate bundle for the signers that will
// be recognized for incoming client certificates in addition to the operator managed signers.
func observeUserClientCABundle(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, syncActionRules, []error) {
	configMapName := apiServer.Spec.ClientCA.Name
	if len(configMapName) == 0 {
		return nil, nil, nil // previously observed resource (if any) should be deleted
	}
	// The user managed client CA bundle will be combined with other operator managed client CA bundles (by the target
	// config controller) into a common client CA bundle managed by the operator. As such, since the user managed client
	// CA bundle is never explicitly referenced in the kube-apiserver config, the returned observed config will always
	// be empty.
	return nil, syncActionRules{"user-client-ca": configMapName}, nil
}

// observeNamedCertificates observes user managed Secrets containing TLS cert info for serving secure traffic to
// specific hostnames.
func observeNamedCertificates(apiServer *configv1.APIServer, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ syncActionRules, _ []error) {
	namedCertificatesPath := []string{"servingInfo", "namedCertificates"}
	defer func() {
		ret = configobserver.Pruned(ret, namedCertificatesPath)
	}()

	var errs []error
	observedConfig := map[string]interface{}{}

	namedCertificates := apiServer.Spec.ServingCerts.NamedCertificates
	if len(namedCertificates) > maxUserNamedCerts {
		// TODO: This should be validation error, not operator error/event.
		err := fmt.Errorf("spec.servingCerts.namedCertificates cannot have more than %d entries", maxUserNamedCerts)
		recorder.Warningf("ObserveNamedCertificatesFailed", err.Error())
		return existingConfig, nil, append(errs, err)
	}

	// add the named cert info to the observed config. return the previously observed config on any error.

	resourceSyncRules := syncActionRules{}
	var observedNamedCertificates []interface{}

	// these are always present in the config because we mint and rotate them ourselves.
	observedNamedCertificates = append(observedNamedCertificates, map[string]interface{}{
		"certFile": "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.crt",
		"keyFile":  "/etc/kubernetes/static-pod-certs/secrets/localhost-serving-cert-certkey/tls.key"})
	observedNamedCertificates = append(observedNamedCertificates, map[string]interface{}{
		"certFile": "/etc/kubernetes/static-pod-certs/secrets/service-network-serving-certkey/tls.crt",
		"keyFile":  "/etc/kubernetes/static-pod-certs/secrets/service-network-serving-certkey/tls.key"})
	observedNamedCertificates = append(observedNamedCertificates, map[string]interface{}{
		"certFile": "/etc/kubernetes/static-pod-certs/secrets/external-loadbalancer-serving-certkey/tls.crt",
		"keyFile":  "/etc/kubernetes/static-pod-certs/secrets/external-loadbalancer-serving-certkey/tls.key"})
	observedNamedCertificates = append(observedNamedCertificates, map[string]interface{}{
		"certFile": "/etc/kubernetes/static-pod-certs/secrets/internal-loadbalancer-serving-certkey/tls.crt",
		"keyFile":  "/etc/kubernetes/static-pod-certs/secrets/internal-loadbalancer-serving-certkey/tls.key"})
	observedNamedCertificates = append(observedNamedCertificates, map[string]interface{}{
		"certFile": "/etc/kubernetes/static-pod-resources/secrets/localhost-recovery-serving-certkey/tls.crt",
		"keyFile":  "/etc/kubernetes/static-pod-resources/secrets/localhost-recovery-serving-certkey/tls.key"})

	// specifiedNameToIndex has keys that are namedCertificate.Names and values that are the index they are used in.
	// we use this to detect if the same name is specified multiple times and fail.
	specifiedNameToIndex := map[string][]string{}
	for index, namedCertificate := range namedCertificates {
		observedNamedCertificate := map[string]interface{}{}
		if len(namedCertificate.Names) > 0 {
			if err := unstructured.SetNestedStringSlice(observedNamedCertificate, namedCertificate.Names, "names"); err != nil {
				return existingConfig, nil, append(errs, err)
			}
		}
		for _, name := range namedCertificate.Names {
			specifiedNameToIndex[name] = append(specifiedNameToIndex[name], fmt.Sprintf("%d", index))
		}

		sourceSecretName := namedCertificate.ServingCertificate.Name
		if len(sourceSecretName) == 0 {
			err := fmt.Errorf("spec.servingCerts.namedCertificates[%d].servingCertificate.name cannot be empty", index)
			recorder.Warningf("ObserveNamedCertificatesFailed", err.Error())
			return existingConfig, nil, append(errs, err)
		}
		// pick one of the available target resource names
		targetSecretName := fmt.Sprintf(namedUserServingCertResourceNameFormat, index)

		// add a sync rule to copy the user specified secret to the operator namespace
		resourceSyncRules[targetSecretName] = sourceSecretName

		// add the named certificate to the observed config
		certFile := fmt.Sprintf("/etc/kubernetes/static-pod-certs/secrets/%s/tls.crt", targetSecretName)
		if err := unstructured.SetNestedField(observedNamedCertificate, certFile, "certFile"); err != nil {
			return existingConfig, nil, append(errs, err)
		}

		keyFile := fmt.Sprintf("/etc/kubernetes/static-pod-certs/secrets/%s/tls.key", targetSecretName)
		if err := unstructured.SetNestedField(observedNamedCertificate, keyFile, "keyFile"); err != nil {
			return existingConfig, nil, append(errs, err)
		}

		observedNamedCertificates = append(observedNamedCertificates, observedNamedCertificate)
	}

	for name, indexes := range specifiedNameToIndex {
		if len(indexes) == 1 {
			continue
		}
		errs = append(errs, fmt.Errorf("spec.servingCerts.namedCertificates[...].servingCertificate.name %q is used by other indexes: [%s]", name, strings.Join(indexes, ",")))
	}

	if len(observedNamedCertificates) > 0 {
		if err := unstructured.SetNestedField(observedConfig, observedNamedCertificates, namedCertificatesPath...); err != nil {
			return existingConfig, nil, append(errs, err)
		}
	}

	return observedConfig, resourceSyncRules, errs
}

type apiServerObserver struct {
	observerFunc  observeAPIServerConfigFunc
	configPaths   [][]string
	resourceNames []string
	resourceType  interface{}
}

func (o *apiServerObserver) observe(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	var errs []error

	// pick the correct resource sync function
	resourceSync := listers.ResourceSyncer().SyncSecret
	if _, ok := o.resourceType.(corev1.ConfigMap); ok {
		resourceSync = listers.ResourceSyncer().SyncConfigMap
	}

	previouslyObservedConfig, errs := extractPreviouslyObservedConfig(existingConfig, o.configPaths...)

	apiServer, err := listers.APIServerLister().Get("cluster")
	if errors.IsNotFound(err) {
		// no error, just clear the observed config and observed resources
		return nil, append(errs, syncObservedResources(resourceSync, deleteSyncRules(o.resourceNames...))...)
	}

	// if something went wrong, keep the previously observed config and resources
	if err != nil {
		klog.Warningf("error getting apiservers.%s/cluster: %v", configv1.GroupName, err)
		return previouslyObservedConfig, append(errs, err)
	}

	observedConfig, observedResources, errs := o.observerFunc(apiServer, recorder, previouslyObservedConfig)

	// if we get error during observation, skip the merging and return previous config and errors.
	if len(errs) > 0 {
		klog.Warningf("errors during apiservers.%s/cluster processing: %+v", configv1.GroupName, errs)
		return previouslyObservedConfig, errs
	}

	// default to deleting previous resources, and then merge in observed resources rules
	resourceSyncRules := deleteSyncRules(o.resourceNames...)
	if err := mergo.Merge(&resourceSyncRules, &observedResources, mergo.WithOverride); err != nil {
		klog.Warningf("merging resource sync rules failed: %v", err)
	}

	errs = append(errs, syncObservedResources(resourceSync, resourceSyncRules)...)

	return observedConfig, errs
}

// deleteSyncRules generates resource sync rules to delete the resources, specified by names, from the
// operator namespace.
func deleteSyncRules(names ...string) syncActionRules {
	resourceSyncRules := syncActionRules{}
	for _, name := range names {
		// empty string here means there is "from" anymore and we mark the "name" for deletion.
		resourceSyncRules[name] = ""
	}
	return resourceSyncRules
}

// syncObservedResources copies or deletes resources, sources in GlobalUserSpecifiedConfigNamespace and destinations in OperatorNamespace namespace.
// Errors are collected, i.e. it's not failing on first error.
func syncObservedResources(syncResource resourceSyncFunc, syncRules syncActionRules) []error {
	var errs []error
	for to, from := range syncRules {
		var source resourcesynccontroller.ResourceLocation
		if len(from) > 0 {
			source = resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: from}
		}
		// if 'from' is empty, then it means we want to delete
		destination := resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: to}
		if err := syncResource(destination, source); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// extractPreviouslyObservedConfig extracts the previously observed config from the existing config.
func extractPreviouslyObservedConfig(existing map[string]interface{}, paths ...[]string) (map[string]interface{}, []error) {
	var errs []error
	previous := map[string]interface{}{}
	for _, fields := range paths {
		value, found, err := unstructured.NestedFieldCopy(existing, fields...)
		if !found {
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
		err = unstructured.SetNestedField(previous, value, fields...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return previous, errs
}
