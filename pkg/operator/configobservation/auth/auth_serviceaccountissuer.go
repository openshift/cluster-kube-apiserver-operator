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

// defaultServiceAccountIssuerValue is a value used when no service account issuer is configured.
// This is in sync with bootstrap and post-bootstrap config overrides.
const defaultServiceAccountIssuerValue = "https://kubernetes.default.svc"

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
	var existingConfigIssuer, observedActiveIssuer string
	// when the issuer will change, indicate that by setting `issuerChanged` to true
	// to emit the informative event
	defer func() {
		if issuerChanged {
			recorder.Eventf(
				"ObserveServiceAccountIssuer",
				"ServiceAccount issuer changed from %v to %v",
				existingConfigIssuer, observedActiveIssuer,
			)
		}
	}()

	existingConfigIssuers, _, err := unstructured.NestedStringSlice(existingConfig, serviceAccountIssuerPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract service account issuer from unstructured: %v", err))
	}

	for i := range existingConfigIssuers {
		if len(existingConfigIssuers[i]) > 0 {
			existingConfigIssuer = existingConfigIssuers[i]
			break
		}
	}

	// If the issuer is not set, it is safe to assume it is being defaulted by the config override.
	// However, if there is no issuer set in KAS, we need to default it here and also change the "service-account-jwks-uri".
	if len(existingConfigIssuer) == 0 {
		existingConfigIssuer = defaultServiceAccountIssuerValue
		existingConfigIssuers = []string{existingConfigIssuer}
	}

	operator, err := getOperator("cluster")
	if apierrors.IsNotFound(err) {
		klog.Warningf("kubeapiserver.operators.openshift.io/cluster: not found")
		operator = &operatorv1.KubeAPIServer{}
	} else if err != nil {
		return existingConfig, append(errs, err)
	}

	// observedActiveIssuer is the desired service account issuer set in KAS operator status
	// This apiServerArgumentValue is being synced using serviceaccountissuer controller.
	observedActiveIssuer = getActiveServiceAccountIssuer(operator.Status.ServiceAccountIssuers)
	// if desired active issuer is not set (the serviceaccountissuer for some reason has not defaulted it)
	// then make sure, we default it here, because we have to set the jwks-uri correctly.
	if existingConfigIssuer == defaultServiceAccountIssuerValue && len(observedActiveIssuer) == 0 {
		observedActiveIssuer = existingConfigIssuer
	}
	if err := checkIssuer(observedActiveIssuer); err != nil {
		return existingConfig, append(errs, err)
	}

	desiredTrustedIssuers := getTrustedServiceAccountIssuers(operator.Status.ServiceAccountIssuers)

	// here we compare the issuers that exists in KAS apiArguments and desired issuers in KAS-O status.
	issuerChanged = issuersChanged(
		existingConfigIssuers,
		append([]string{observedActiveIssuer}, desiredTrustedIssuers...)...,
	)

	// the desired active issuer MUST always be the first in the list
	apiServerArgumentValue := []interface{}{observedActiveIssuer}
	// then trusted issuers follow
	for i := range desiredTrustedIssuers {
		apiServerArgumentValue = append(apiServerArgumentValue, desiredTrustedIssuers[i])
	}
	apiServerArguments := map[string]interface{}{
		"service-account-issuer": apiServerArgumentValue,
		"api-audiences":          apiServerArgumentValue,
	}

	// If the issuer is not set in KAS, we rely on the config-overrides.yaml to set both
	// the issuer and the api-audiences but configure the jwks-uri to point to
	// the LB so that it does not default to KAS IP which is not included in the serving certs
	if observedActiveIssuer == defaultServiceAccountIssuerValue {
		infrastructureConfig, err := getInfrastructureConfig("cluster")
		if err != nil {
			return existingConfig, append(errs, err)
		}
		if apiServerInternalURL := infrastructureConfig.Status.APIServerInternalURL; len(apiServerInternalURL) == 0 {
			return existingConfig, append(errs, fmt.Errorf("APIServerInternalURL missing from infrastructure/cluster"))
		} else {
			apiServerArguments["service-account-jwks-uri"] = []interface{}{apiServerInternalURL + "/openid/v1/jwks"}
		}
	}

	return map[string]interface{}{"apiServerArguments": apiServerArguments}, errs

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
