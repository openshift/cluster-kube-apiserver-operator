package auth

import (
	"fmt"
	"net/url"
	"strings"

	"k8s.io/klog/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

var (
	serviceAccountIssuerPath = []string{"apiServerArguments", "service-account-issuer"}
	audiencesPath            = []string{"apiServerArguments", "api-audiences"}
	jwksURIPath              = []string{"apiServerArguments", "service-account-jwks-uri"}
)

// ObserveServiceAccountIssuer changes apiServerArguments.service-account-issuer from
// the default value if Authentication.Spec.ServiceAccountIssuer specifies a valid
// non-empty value.
func ObserveServiceAccountIssuer(
	genericListers configobserver.Listers,
	recorder events.Recorder,
	existingConfig map[string]interface{},
) (map[string]interface{}, []error) {

	listers := genericListers.(configobservation.Listers)
	ret, errs := observedConfig(existingConfig, listers.AuthConfigLister.Get, listers.InfrastructureLister().Get, recorder)
	return configobserver.Pruned(ret, serviceAccountIssuerPath, audiencesPath, jwksURIPath), errs
}

// observedConfig returns an unstructured fragment of KubeAPIServerConfig that may
// include an override of the default service account issuer if one was set in the
// Authentication resource.
func observedConfig(
	existingConfig map[string]interface{},
	getAuthConfig func(string) (*configv1.Authentication, error),
	getInfrastructureConfig func(string) (*configv1.Infrastructure, error),
	recorder events.Recorder,
) (map[string]interface{}, []error) {

	errs := []error{}
	var issuerChanged bool
	var existingIssuer, newIssuer string
	// when the issuer will change, indicate that by setting `issuerChanged` to true
	// to emit the informative event
	defer func() {
		if issuerChanged {
			recorder.Eventf(
				"ObserveServiceAccountIssuer",
				"ServiceAccount issuer changed from %v to %v",
				existingIssuer, newIssuer,
			)
		}
	}()

	existingIssuers, _, err := unstructured.NestedStringSlice(existingConfig, serviceAccountIssuerPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract service account issuer from unstructured: %v", err))
	}

	if len(existingIssuers) > 0 {
		existingIssuer = existingIssuers[0]
	}

	authConfig, err := getAuthConfig("cluster")
	if apierrors.IsNotFound(err) {
		klog.Warningf("authentications.config.openshift.io/cluster: not found")
		// No issuer if the auth config is missing
		authConfig = &configv1.Authentication{}
	} else if err != nil {
		return existingConfig, append(errs, err)
	}

	newIssuer = authConfig.Spec.ServiceAccountIssuer
	if err := checkIssuer(newIssuer); err != nil {
		return existingConfig, append(errs, err)
	}

	if len(newIssuer) != 0 {
		issuerChanged = existingIssuer != newIssuer
		// configure the issuer if set by the user and is a valid issuer
		return map[string]interface{}{
			"apiServerArguments": map[string]interface{}{
				"service-account-issuer": []interface{}{
					newIssuer,
				},
				"api-audiences": []interface{}{
					newIssuer,
				},
			},
		}, errs
	}

	// if the issuer is not set, rely on the config-overrides.yaml to set both
	// the issuer and the api-audiences but configure the jwks-uri to point to
	// the LB so that it does not default to KAS IP which is not included
	// in the serving certs
	infrastructureConfig, err := getInfrastructureConfig("cluster")
	if err != nil {
		return existingConfig, append(errs, err)
	}
	apiServerInternalURL := infrastructureConfig.Status.APIServerInternalURL
	if len(apiServerInternalURL) == 0 {
		return existingConfig, append(errs, fmt.Errorf("APIServerInternalURL missing from infrastructure/cluster"))
	}

	issuerChanged = existingIssuer != newIssuer
	return map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"service-account-jwks-uri": []interface{}{
				apiServerInternalURL + "/openid/v1/jwks",
			},
		},
	}, errs
}

// checkIssuer validates the issuer in the same way that it will be validated by
// kube-apiserver
func checkIssuer(issuer string) error {
	if !strings.Contains(issuer, ":") {
		return nil
	}
	// If containing a colon, must parse without error as a url
	_, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("service-account issuer contained a ':' but was not a valid URL: %v", err)
	}
	return nil
}
