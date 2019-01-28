package apiserver

import (
	"fmt"

	"github.com/golang/glog"
	"github.com/imdario/mergo"

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
	userServingCertPublicCertFile            = "/etc/kubernetes/static-pod-resources/secrets/user-serving-cert/tls.crt"
	userServingCertPrivateKeyFile            = "/etc/kubernetes/static-pod-resources/secrets/user-serving-cert/tls.key"
	maxUserNamedCerts                        = 10
	userServingCertResourceName              = "user-serving-cert"
	namedUserServingCertResourceNameFormat   = "user-serving-cert-%03d"
	namedUserServingCertPublicCertFileFormat = "/etc/kubernetes/static-pod-resources/secrets/%v/tls.crt"
	namedUserServingCertPrivateKeyFileFormat = "/etc/kubernetes/static-pod-resources/secrets/%v/tls.key"
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

// resourceSyncFunc syncs a resource from the source location to the destination location.
type resourceSyncFunc func(destination, source resourcesynccontroller.ResourceLocation) error

// observeAPIServerConfigFunc observes configuration and returns the observedConfig and a map describing a list of
// resources to sync.
type observeAPIServerConfigFunc func(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, map[string]*string, []error)

// ObserveUserClientCABundle returns an ObserveConfigFunc that observes a user managed certificate bundle containing
// signers that will be recognized for incoming client certificates in addition to the operator managed signers.
func ObserveUserClientCABundle() configobserver.ObserveConfigFunc {
	return newAPIServerObserver(observeUserClientCABundle, [][]string{}, []string{"user-client-ca"}, corev1.ConfigMap{})
}

// ObserveDefaultUserServingCertificate returns an ObserveConfigFunc that observes user managed TLS cert info for
// serving secure traffic.
func ObserveDefaultUserServingCertificate() configobserver.ObserveConfigFunc {
	configPaths := [][]string{{"servingInfo", "certFile"}, {"servingInfo", "keyFile"}}
	return newAPIServerObserver(observeDefaultUserServingCertificate, configPaths, []string{userServingCertResourceName}, corev1.ConfigMap{})
}

// ObserveNamedCertificates returns an ObserveConfigFunc that observes user managed TLS cert info for serving secure
// traffic to specific hostnames.
func ObserveNamedCertificates() configobserver.ObserveConfigFunc {
	configPaths := [][]string{{"servingInfo", "namedCertificates"}}
	return newAPIServerObserver(observeNamedCertificates, configPaths, namedUserServingCertResourceNames, corev1.Secret{})
}

// observeUserClientCABundle observes a user managed ConfigMap containing a certificate bundle for the signers that will
// be recognized for incoming client certificates in addition to the operator managed signers.
func observeUserClientCABundle(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, map[string]*string, []error) {
	configMapName := apiServer.Spec.ClientCA.Name
	if len(configMapName) == 0 {
		return nil, nil, nil // previously observed resource (if any) should be deleted
	}
	// The user managed client CA bundle will be combined with other operator managed client CA bundles (by the target
	// config controller) into a common client CA bundle managed by the operator. As such, since the user managed client
	// CA bundle is never explicitly referenced in the kube-apiserver config, the returned observed config will always
	// be empty.
	return nil, map[string]*string{"user-client-ca": &configMapName}, nil
}

// observeDefaultUserServingCertificate observes user managed Secret containing the default cert info for serving
// secure traffic.
func observeDefaultUserServingCertificate(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, map[string]*string, []error) {
	var errs []error
	servingCertSecretName := apiServer.Spec.ServingCerts.DefaultServingCertificate.Name
	if len(servingCertSecretName) == 0 {
		return nil, nil, nil // previously observed config and resources (if any) should be deleted
	}
	// generate an observed configuration that will configure the kube-apiserver to use the user managed default serving
	// cert info instead of the operator managed default serving cert info.
	observedConfig := map[string]interface{}{}
	certFile := fmt.Sprint(userServingCertPublicCertFile)
	if err := unstructured.SetNestedField(observedConfig, certFile, "servingInfo", "certFile"); err != nil {
		return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(userServingCertResourceName), append(errs, err)
	}
	keyFile := fmt.Sprint(userServingCertPrivateKeyFile)
	if err := unstructured.SetNestedField(observedConfig, keyFile, "servingInfo", "keyFile"); err != nil {
		return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(userServingCertResourceName), append(errs, err)
	}
	return observedConfig, map[string]*string{"user-serving-cert": &servingCertSecretName}, errs
}

// observeNamedCertificates observes user managed Secrets containing TLS cert info for serving secure traffic to
// specific hostnames.
func observeNamedCertificates(apiServer *configv1.APIServer, recorder events.Recorder, previouslyObservedConfig map[string]interface{}) (map[string]interface{}, map[string]*string, []error) {
	var errs []error

	observedConfig := map[string]interface{}{}

	namedCertificates := apiServer.Spec.ServingCerts.NamedCertificates
	if len(namedCertificates) > maxUserNamedCerts {
		err := fmt.Errorf("apiservers.config.openshift.io/cluster: spec.servingCerts.namedCertificates cannot have more than %v entries", maxUserNamedCerts)
		recorder.Warningf("ObserveNamedCertificatesFailed", err.Error())
		return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
	}

	// add the named cert info to the observed config. return the previously observed config on any error.
	namedCertificatesPath := []string{"servingInfo", "namedCertificates"}
	resourceSyncRules := map[string]*string{}
	if len(namedCertificates) > 0 {
		var observedNamedCertificates []interface{}
		for index, namedCertificate := range namedCertificates {
			observedNamedCertificate := map[string]interface{}{}
			if len(namedCertificate.Names) > 0 {
				if err := unstructured.SetNestedStringSlice(observedNamedCertificate, namedCertificate.Names, "names"); err != nil {
					return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
				}
			}
			sourceSecretName := namedCertificate.ServingCertificate.Name
			if len(sourceSecretName) == 0 {
				err := fmt.Errorf("apiservers.config.openshift.io/cluster: spec.servingCerts.namedCertificates[%v].servingCertificate.name not found", index)
				recorder.Warningf("ObserveNamedCertificatesFailed", err.Error())
				return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
			}
			// pick one of the available target resource names
			targetSecretName := fmt.Sprintf(namedUserServingCertResourceNameFormat, index)

			// add a sync rule to copy the user specified secret to the operator namespace
			resourceSyncRules[targetSecretName] = &sourceSecretName

			// add the named certificate to the observed config
			certFile := fmt.Sprintf(namedUserServingCertPublicCertFileFormat, targetSecretName)
			if err := unstructured.SetNestedField(observedNamedCertificate, certFile, "certFile"); err != nil {
				return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
			}
			keyFile := fmt.Sprintf(namedUserServingCertPrivateKeyFileFormat, targetSecretName)
			if err := unstructured.SetNestedField(observedNamedCertificate, keyFile, "keyFile"); err != nil {
				return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
			}
			observedNamedCertificates = append(observedNamedCertificates, observedNamedCertificate)
		}
		if err := unstructured.SetNestedField(observedConfig, observedNamedCertificates, namedCertificatesPath...); err != nil {
			return previouslyObservedConfig, makeIgnoreAllResourcesSyncRules(namedUserServingCertResourceNames...), append(errs, err)
		}
	}

	return observedConfig, resourceSyncRules, errs
}

// newAPIServerObserver returns an ObserveConfigFunc that observes configuration and resources.
func newAPIServerObserver(observeAPIServerConfig observeAPIServerConfigFunc, configPaths [][]string, resourceNames []string, resourceType interface{}) configobserver.ObserveConfigFunc {
	return func(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
		listers := genericListers.(configobservation.Listers)
		var errs []error

		// pick the correct resource sync function
		resourceSync := listers.ResourceSyncer().SyncSecret
		if _, ok := resourceType.(corev1.ConfigMap); ok {
			resourceSync = listers.ResourceSyncer().SyncConfigMap
		}

		// extract the previously observed config. capture, but don't react to, errors.
		previouslyObservedConfig, errs := extractPreviouslyObservedConfig(existingConfig, configPaths...)

		// get the apiserver config
		apiServer, err := listers.APIServerLister.Get("cluster")

		// if apiserver config is not found, it's not an error (it's optional), clear the observed config and observed resources
		if errors.IsNotFound(err) {
			glog.Warningf("apiservers.config.openshift.io/cluster: not found")
			deleteObservedResources(resourceSync, resourceNames)
			return nil, errs
		}

		// if something went wrong, keep the previously observed config and resources
		if err != nil {
			return previouslyObservedConfig, append(errs, err)
		}

		observedConfig, observedResources, errs := observeAPIServerConfig(apiServer, recorder, previouslyObservedConfig)

		// default to deleting previous resources, and then merge in observed resources rules
		resourceSyncRules := makeDeleteAllResourcesSyncRules(resourceNames...)
		if err := mergo.Merge(&resourceSyncRules, &observedResources, mergo.WithOverride); err != nil {
			glog.Warningf("merging resource sync rules failed: %v", err)
		}

		// sync observed resources
		errs = append(errs, syncObservedResources(resourceSync, resourceSyncRules)...)

		return observedConfig, errs
	}
}

// makeDeleteAllResourcesSyncRules generates resource sync rules to delete the resources, specified by names, from the
// operator namespace.
func makeDeleteAllResourcesSyncRules(names ...string) map[string]*string {
	resourceSyncRules := map[string]*string{}
	deleteIndicator := ""
	for _, name := range names {
		resourceSyncRules[name] = &deleteIndicator
	}
	return resourceSyncRules
}

// makeIgnoreAllResourcesSyncRules generates resource sync rules to ignore the resources, specified by names, from the
// operator namespace. This is useful if you need to, for example, leave all the previously observed resources as they
// are.
func makeIgnoreAllResourcesSyncRules(names ...string) map[string]*string {
	resourceSyncRules := map[string]*string{}
	for _, name := range names {
		resourceSyncRules[name] = nil
	}
	return resourceSyncRules
}

// syncObservedResources synchronizes resource from the global user specified config namespace into the operator
// namespace. The the entry keys in the syncRules should be the names of the resources to be synced into the operator
// namespace. The corresponding values should be either the name of the resource to copy from the global user specified
// config namespace to the operator's namespace on sync, an empty string to indicate that the resource should be deleted
// on sync, or a nil to indicate that the resource should be left as-is on sync.
func syncObservedResources(syncResource resourceSyncFunc, syncRules map[string]*string) []error {
	var errs []error
	for to, from := range syncRules {
		if from != nil {
			var source resourcesynccontroller.ResourceLocation
			if len(*from) > 0 {
				source = resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: *from}
			}
			destination := resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: to}
			if err := syncResource(destination, source); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// deleteObservedResources delete the resources, specified by names, from the operator namespace.
func deleteObservedResources(syncFunc resourceSyncFunc, names []string) []error {
	return syncObservedResources(syncFunc, makeDeleteAllResourcesSyncRules(names...))
}

// extractPreviouslyObservedConfig extracts the previously observed config from the existing config.
func extractPreviouslyObservedConfig(existing map[string]interface{}, paths ...[]string) (map[string]interface{}, []error) {
	var errs []error
	previous := map[string]interface{}{}
	for _, fields := range paths {
		value, _, err := unstructured.NestedFieldCopy(existing, fields...)
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
