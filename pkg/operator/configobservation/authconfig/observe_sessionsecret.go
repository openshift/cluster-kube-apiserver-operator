package authconfig

import (
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	sessionSecretNamespace = "openshift-kube-apiserver"
	sessionSecretName      = "session-secret"
	sessionSecretPath      = "/etc/kubernetes/static-pod-resources/secrets/session-secret/secret"
)

// ObserveSessionSecret sets/unsets the oauthConfig sessionSecretsFile depending on if session-secret exists.
func ObserveSessionSecret(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	oauthConfigSessionSecretsFilePath := []string{"oauthConfig", "sessionConfig", "sessionSecretsFile"}
	currentSessionSecretsFilePath, _, err := unstructured.NestedString(existingConfig, oauthConfigSessionSecretsFilePath...)
	if err != nil {
		errs = append(errs, err)
	}
	if len(currentSessionSecretsFilePath) > 0 {
		if err := unstructured.SetNestedField(prevObservedConfig, currentSessionSecretsFilePath, oauthConfigSessionSecretsFilePath...); err != nil {
			errs = append(errs, err)
		}
	}

	if !listers.SecretHasSynced() {
		glog.Warningf("secrets not synced")
		return prevObservedConfig, errs
	}

	observedConfig := map[string]interface{}{}
	_, err = listers.SecretLister.Secrets(sessionSecretNamespace).Get(sessionSecretName)
	if errors.IsNotFound(err) {
		glog.Warningf("session secret %s/%s not found", sessionSecretNamespace, sessionSecretName)
		// Unset the value if we need to.
		if len(currentSessionSecretsFilePath) > 0 {
			err := unstructured.SetNestedField(observedConfig, "", oauthConfigSessionSecretsFilePath...)
			if err != nil {
				errs = append(errs, err)
			}
		}
		return observedConfig, errs
	}
	if err != nil {
		return prevObservedConfig, errs
	}

	// Secret is found and sessionSecretPath is already set.
	if len(currentSessionSecretsFilePath) > 0 {
		return prevObservedConfig, errs
	}

	// Set sessionSecretPath
	err = unstructured.SetNestedField(observedConfig, sessionSecretPath, oauthConfigSessionSecretsFilePath...)
	if err != nil {
		errs = append(errs, err)
	}

	return observedConfig, errs
}
