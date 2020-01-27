package auth

import (
	"fmt"
	"net/url"
	"strings"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

// ObserveServiceAccountIssuer changes apiServerArguments.service-account-issuer from
// the default value if Authentication.Spec.ServiceAccountIssuer specifies a valid
// non-empty value.
func ObserveServiceAccountIssuer(
	genericListers configobserver.Listers,
	_ events.Recorder,
	existingConfig map[string]interface{},
) (map[string]interface{}, []error) {

	listers := genericListers.(configobservation.Listers)
	return observedConfig(existingConfig, listers.AuthConfigLister.Get)
}

// observedConfig returns an unstructured fragment of KubeAPIServerConfig that may
// include an override of the default service account issuer if one was set in the
// Authentication resource.
func observedConfig(
	existingConfig map[string]interface{},
	authConfigAccessor func(string) (*v1.Authentication, error),
) (map[string]interface{}, []error) {

	issuer, errs := observedIssuer(existingConfig, authConfigAccessor)
	config := unstructuredConfigForIssuer(issuer)
	return config, errs
}

// observedIssuer attempts to source the service account issuer defined in the
// Authentication resource named "cluster". If that is not possible (due to error or
// validation failure), an attempt will be made to return the previously observed
// issuer. If that is not possible, then an empty string will be returned.
func observedIssuer(
	existingConfig map[string]interface{},
	authConfigAccessor func(string) (*v1.Authentication, error),
) (string, []error) {
	errs := []error{}

	previousIssuer, moreErrs := issuerFromUnstructuredConfig(existingConfig)
	errs = append(errs, moreErrs...)

	authConfig, err := authConfigAccessor("cluster")
	if apierrors.IsNotFound(err) {
		klog.Warningf("authentications.config.openshift.io/cluster: not found")
		// No issuer if the auth config is missing
		return "", errs
	} else if err != nil {
		// Return the previously observed issuer if an error prevented retrieving the auth config
		errs = append(errs, err)
		return previousIssuer, errs
	}

	newIssuer := authConfig.Spec.ServiceAccountIssuer
	err = checkIssuer(newIssuer, "spec.serviceAccountIssuer")
	if err != nil {
		// Return the previous issuer if the new issuer fails validation
		errs = append(errs, err)
		return previousIssuer, errs
	}
	return newIssuer, errs
}

// issuerFromUnstructuredConfig extracts the service account issuer from the provided
// unstructured KubeAPIServerConfig fragment.
func issuerFromUnstructuredConfig(config map[string]interface{}) (string, []error) {
	errs := []error{}
	fields := []string{"apiServerArguments", "service-account-issuer"}
	qualifiedField := strings.Join(fields, ".")

	previousIssuers, _, err := unstructured.NestedStringSlice(config, fields...)
	if err != nil {
		errs = append(errs, fmt.Errorf("Unable to extract %s from unstructured: %v", qualifiedField, err))
	}

	previousIssuer := ""
	issuerCount := len(previousIssuers)
	if issuerCount > 1 {
		klog.Warningf("%s specified more than one issuer. Only the first issuer will be used.", qualifiedField)
	}
	if issuerCount > 0 {
		err := checkIssuer(previousIssuers[0], qualifiedField)
		if err != nil {
			errs = append(errs, err)
		} else {
			previousIssuer = previousIssuers[0]
		}
	}
	return previousIssuer, errs
}

// unstructuredConfigForIssuer creates an unstructured KubeAPIServerConfig fragment
// for the given issuer.
func unstructuredConfigForIssuer(issuer string) map[string]interface{} {
	// Returning an empty config when no value is provided for issuer ensures that
	// the default value will not be overwritten by an empty value.
	if len(issuer) == 0 {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"service-account-issuer": []string{
				issuer,
			},
		},
	}
}

// checkIssuer validates the issuer in the same way that it will be validated by
// kube-apiserver. Performing this validation here allows the error to be reported as
// a condition rather than via a failing pod.
func checkIssuer(issuer, fieldName string) error {
	// Does not contain a colon
	if !strings.Contains(issuer, ":") {
		return nil
	}
	// If containing a colon, must parse without error as a url
	_, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("%s contained a ':' but was not a valid URL: %v", fieldName, err)
	}
	return nil
}
