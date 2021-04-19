package oauth

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

// OAuthLister lists OAuth information
type OAuthLister interface {
	OAuthLister() configlistersv1.OAuthLister
}

const (
	fieldAccessTokenInactivityTimeout = "accessTokenInactivityTimeout"
)

func IsWebhookAuthenticatorConfigured(listers configobservation.Listers, existingConfig map[string]interface{}) (bool, error) {
	auth, err := listers.AuthConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return true, err
	}

	webhookSecretName := ""
	if auth.Spec.WebhookTokenAuthenticator != nil {
		webhookSecretName = auth.Spec.WebhookTokenAuthenticator.KubeConfig.Name
	}

	if len(webhookSecretName) > 0 {
		return true, nil
	}

	return false, nil
}

// ObserveAccessTokenInactivityTimeout returns an unstructured fragment of KubeAPIServerConfig that has access token inactivity timeout,
// if there is a valid value for it in OAuth cluster config.
func ObserveAccessTokenInactivityTimeout(genericlisters configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	errs = []error{}
	tokenConfigPath := []string{"oauthConfig", "tokenConfig"}
	tokenInactivityTimeoutPath := append(tokenConfigPath, fieldAccessTokenInactivityTimeout)
	defer func() {
		// Prune the observed config so that it only contains access token inactivity timeout field.
		ret = configobserver.Pruned(ret, tokenInactivityTimeoutPath)
	}()

	listers, ok := genericlisters.(configobservation.Listers)
	if !ok {
		return existingConfig, append(errs, fmt.Errorf("failed to assert: given lister does not implement OAuth lister"))
	}

	if isReady, err := IsWebhookAuthenticatorConfigured(listers, existingConfig); err != nil {
		// if we had an error determining, just show the error and keep the existing config.
		return existingConfig, append(errs, err)
	} else if isReady {
		// if the webhook is ready, then we should no longer have any configuration for kube-apiserver
		return map[string]interface{}{}, nil
	}

	oauthConfig, err := listers.OAuthLister().Get("cluster")
	if err != nil {
		// Failed to read OAuth cluster config.
		if errors.IsNotFound(err) {
			klog.Warning("oauth.config.openshift.io/cluster: not found")
		}
		// Return whatever is present in existing config
		return existingConfig, append(errs, err)
	}

	existingAccessTokenInactivityTimeout, _, err := unstructured.NestedString(existingConfig, tokenInactivityTimeoutPath...)
	if err != nil {
		errs = append(errs, err)
	}

	observedConfig := map[string]interface{}{}
	observedAccessTokenInactivityTimeout := ""
	if oauthConfig.Spec.TokenConfig.AccessTokenInactivityTimeout != nil {
		observedAccessTokenInactivityTimeout = oauthConfig.Spec.TokenConfig.AccessTokenInactivityTimeout.Duration.String()
		observedConfig = buildUnstructuredTokenConfig(observedAccessTokenInactivityTimeout, tokenInactivityTimeoutPath)
	}

	if existingAccessTokenInactivityTimeout != observedAccessTokenInactivityTimeout {
		recorder.Eventf("ObserveAccessTokenInactivityTimeout", "%s changed from %v to %v", fieldAccessTokenInactivityTimeout,
			existingAccessTokenInactivityTimeout,
			observedAccessTokenInactivityTimeout)
	}

	return observedConfig, errs
}

func buildUnstructuredTokenConfig(val interface{}, fields []string) map[string]interface{} {
	unstructuredConfig := map[string]interface{}{}

	if err := unstructured.SetNestedField(unstructuredConfig, val, fields...); err != nil {
		// SetNestedField can return an error if one of the nesting level is not map[string]interface{}.
		// As unstructuredConfig is empty, this error must never happen.
		klog.Warningf("failed to write unstructured config for fields %v: %v", fields, err)
	}

	return unstructuredConfig
}
