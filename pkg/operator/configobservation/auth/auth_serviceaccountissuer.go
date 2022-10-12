package auth

import (
	"fmt"
	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"net/url"
	"sort"
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
	ret, errs := observedConfig(existingConfig, listers.KubeAPIServerOperatorLister().Get, listers.InfrastructureLister().Get, recorder)
	return configobserver.Pruned(ret, serviceAccountIssuerPath, audiencesPath, jwksURIPath), errs
}

// observedConfig returns an unstructured fragment of KubeAPIServerConfig that may
// include an override of the default service account issuer if one was set in the
// Authentication resource.
func observedConfig(existingConfig map[string]interface{},
	getOperator func(name string) (*operatorv1.KubeAPIServer, error),
	getInfrastructureConfig func(string) (*configv1.Infrastructure, error), recorder events.Recorder) (map[string]interface{}, []error) {

	errs := []error{}
	var issuerChanged bool
	var existingActiveIssuer, newActiveIssuer string
	// when the issuer will change, indicate that by setting `issuerChanged` to true
	// to emit the informative event
	defer func() {
		if issuerChanged {
			recorder.Eventf(
				"ObserveServiceAccountIssuer",
				"ServiceAccount issuer changed from %v to %v",
				existingActiveIssuer, newActiveIssuer,
			)
		}
	}()

	existingIssuers, _, err := unstructured.NestedStringSlice(existingConfig, serviceAccountIssuerPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract service account issuer from unstructured: %v", err))
	}

	if len(existingIssuers) > 0 {
		existingActiveIssuer = existingIssuers[0]
	}

	operator, err := getOperator("cluster")
	if apierrors.IsNotFound(err) {
		klog.Warningf("kubeapiserver.operators.openshift.io/cluster: not found")
		operator = &operatorv1.KubeAPIServer{}
	} else if err != nil {
		return existingConfig, append(errs, err)
	}

	newActiveIssuer = getActiveServiceAccountIssuer(operator.Status.ServiceAccountIssuers)
	if err := checkIssuer(newActiveIssuer); err != nil {
		return existingConfig, append(errs, err)
	}

	if len(newActiveIssuer) > 0 {
		currentTrustedServiceAccountIssuers := getTrustedServiceAccountIssuers(operator.Status.ServiceAccountIssuers)
		issuerChanged = issuersChanged(
			existingIssuers,
			append([]string{newActiveIssuer}, currentTrustedServiceAccountIssuers...)...,
		)

		value := []interface{}{newActiveIssuer}
		for _, i := range currentTrustedServiceAccountIssuers {
			value = append(value, i)
		}
		// configure the issuer if set by the user and is a valid issuer
		return map[string]interface{}{
			"apiServerArguments": map[string]interface{}{
				"service-account-issuer": value,
				"api-audiences":          value,
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

	issuerChanged = existingActiveIssuer != newActiveIssuer
	return map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"service-account-jwks-uri": []interface{}{
				apiServerInternalURL + "/openid/v1/jwks",
			},
		},
	}, errs
}

// issuersChanged compares the command line flags used for KAS and the operator status service account issuers.
// if these two sets are different, we have a change and we need to update the KAS.
func issuersChanged(kasIssuers []string, trustedOperatorIssuers ...string) bool {
	sort.Strings(kasIssuers)
	operatorAllIssuers := trustedOperatorIssuers
	sort.Strings(operatorAllIssuers)
	return !sets.NewString(kasIssuers...).Equal(sets.NewString(operatorAllIssuers...))
}

func getTrustedServiceAccountIssuers(issuers []operatorv1.ServiceAccountIssuerStatus) []string {
	result := []string{}
	for i := range issuers {
		if issuers[i].ExpirationTime != nil {
			result = append(result, issuers[i].Name)
		}
	}
	return result
}

func getActiveServiceAccountIssuer(issuers []operatorv1.ServiceAccountIssuerStatus) string {
	for i := range issuers {
		if issuers[i].ExpirationTime == nil {
			return issuers[i].Name
		}
	}
	return ""
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
