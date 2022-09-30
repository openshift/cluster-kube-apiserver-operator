package auth

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"net/url"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclientv1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
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
	ret, errs := observedConfig(existingConfig, listers.AuthConfigLister.Get, listers.InfrastructureLister().Get, listers.KubeAPIServerOperatorLister().Get, listers.KubeAPIServerOperatorClient, recorder)
	return configobserver.Pruned(ret, serviceAccountIssuerPath, audiencesPath, jwksURIPath), errs
}

// observedConfig returns an unstructured fragment of KubeAPIServerConfig that may
// include an override of the default service account issuer if one was set in the
// Authentication resource.
func observedConfig(
	existingConfig map[string]interface{},
	getAuthConfig func(string) (*configv1.Authentication, error),
	getInfrastructureConfig func(string) (*configv1.Infrastructure, error),
	getKubeApiserverOperator func(string) (*operatorv1.KubeAPIServer, error),
	operatorClient operatorclientv1.KubeAPIServerInterface,
	recorder events.Recorder,
) (map[string]interface{}, []error) {
	errs := []error{}
	var (
		// activeIssuer is the issuer that is now in use (new tokens use this issuer)
		activeIssuer string
		// newIssuer is the issuer user desire to use as active issuer now
		newIssuer string
		// trustedIssuers represents a list of issuers we "trust", but new tokens don't use
		trustedIssuers sets.String
		issuerChanged  bool
	)
	// when the issuer will change, indicate that by setting `issuerChanged` to true
	// to emit the informative event
	defer func() {
		if issuerChanged {
			recorder.Eventf(
				"ObserveServiceAccountIssuer",
				"ServiceAccount issuer changed from %v to %v",
				activeIssuer, newIssuer,
			)
		}
	}()

	existingIssuers, _, err := unstructured.NestedStringSlice(existingConfig, serviceAccountIssuerPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract service account issuer from unstructured: %v", err))
	}

	// first collect all issuers that are currently being used in KAS config
	existingIssuerSet := sets.NewString(existingIssuers...)
	if existingIssuerSet.Len() > 0 {
		// activeIssuer is the first one in the list
		activeIssuer = existingIssuerSet.List()[0]
		// other issuers are trusted
		if existingIssuerSet.Len() > 1 {
			trustedIssuers = sets.NewString(existingIssuerSet.List()[1 : existingIssuerSet.Len()-1]...)
		}
	}

	// second, collect trusted issuers we track in KAS-O status
	var existingTrustedIssuers sets.String
	kubeAPIOperator, err := getKubeApiserverOperator("cluster")
	if err != nil {
		return existingConfig, append(errs, err)
	}
	for _, issuer := range kubeAPIOperator.Status.ServiceAccountIssuers {
		// TODO: expiration/prune
		existingTrustedIssuers.Insert(issuer.Name)
	}

	authConfig, err := getAuthConfig("cluster")
	if apierrors.IsNotFound(err) {
		klog.Warningf("authentications.config.openshift.io/cluster: not found")
		// No issuer if the auth config is missing
		authConfig = &configv1.Authentication{}
	} else if err != nil {
		return existingConfig, append(errs, err)
	}

	// third, get the service account issuer user configured in the auth config
	newIssuer = authConfig.Spec.ServiceAccountIssuer
	if err := checkIssuer(newIssuer); err != nil {
		return existingConfig, append(errs, err)
	}

	// last, if there is an issuer configured in auth config, and the issuer is different than currently active one, we need to update.
	if len(newIssuer) > 0 && activeIssuer != newIssuer {
		issuerChanged = true

		// this is the list of new "trusted" issuers, leading with the active issuer that is now being used
		newTrustedIssuers := append([]string{activeIssuer}, trustedIssuers.List()...)

		// now we need to update the KAS-O status with new list of issuers.
		// in the new list, the first item without expiration is the desired issuer
		// the second item is the previously used issuer, with expiration set to 1 day
		// the rest are issuers we copy from existing KAS-O status.
		kubeAPIOperatorCopy := kubeAPIOperator.DeepCopy()
		newServiceAccountIssuerStatus := []operatorv1.ServiceAccountIssuerStatus{
			{Name: newIssuer},
			{Name: activeIssuer, ExpirationTime: &metav1.Time{Time: time.Now().Add(24 * time.Hour)}},
		}
		for _, prevTrustedIssuer := range kubeAPIOperator.Status.ServiceAccountIssuers {
			// the previosuly active service account issuer now got expiration timestamp above, skip copying it
			if prevTrustedIssuer.ExpirationTime == nil {
				continue
			}
			newServiceAccountIssuerStatus = append(newServiceAccountIssuerStatus, prevTrustedIssuer)
		}

		// now replace the service account issuers in KAS status.
		// NOTE: this must be done before we make "new" config for KAS, because we don't want conflict on update
		kubeAPIOperatorCopy.Status.ServiceAccountIssuers = newServiceAccountIssuerStatus
		if _, err := operatorClient.UpdateStatus(context.TODO(), kubeAPIOperatorCopy, metav1.UpdateOptions{}); err != nil {
			return existingConfig, append(errs, err)
		}

		return map[string]interface{}{
			"apiServerArguments": map[string]interface{}{
				"service-account-issuer": []interface{}{
					append([]string{newIssuer}, newTrustedIssuers...),
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

	issuerChanged = activeIssuer != newIssuer
	return map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"service-account-jwks-uri": []interface{}{
				apiServerInternalURL + "/openid/operatorclientv1/jwks",
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
