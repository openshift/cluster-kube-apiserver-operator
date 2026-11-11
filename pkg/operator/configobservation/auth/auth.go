package auth

import (
	"bytes"
	"fmt"

	"github.com/imdario/mergo"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type authenticationObserver struct {
	featureGateAccessor  featuregates.FeatureGateAccess
	externalOIDCObserver configobserver.ObserveConfigFunc
	webhookObserver      configobserver.ObserveConfigFunc
}

func NewAuthenticationObserver(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&authenticationObserver{
		featureGateAccessor:  featureGateAccessor,
		externalOIDCObserver: NewObserveExternalOIDC(featureGateAccessor),
		webhookObserver:      NewObserveWebhookTokenAuthenticator(featureGateAccessor),
	}).ObserveAuthentication
}

// ObserveAuthentication combines the direct OIDC and webhook token authenticator
// observations into the kube-apiserver's authentication arguments. During migration
// from direct OIDC to webhook OIDC, it coordinates the authentication transition.
// Otherwise, both observers receive the same existing configuration and run even
// if one returns errors.
// Their results are merged without mutating the inputs, and their errors are
// collected. If merging fails, the existing authentication arguments are retained.
func (o *authenticationObserver) ObserveAuthentication(
	genericListers configobserver.Listers,
	recorder events.Recorder,
	existingConfig map[string]any,
) (ret map[string]any, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, authConfigPath, webhookTokenAuthenticatorPath, webhookTokenAuthenticatorVersionPath)
	}()

	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		// if we haven't observed featuregates yet, return the existing
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, []error{err}
	}

	// check if we are migrating from direct-OIDC to webhook-OIDC
	if featureGates.Enabled(features.FeatureGateExternalOIDC) &&
		externalOIDCWebhookArchitectureRequired(featureGates) {

		listers := genericListers.(configobservation.Listers)
		auth, err := listers.AuthConfigLister.Get("cluster")
		if err != nil && !errors.IsNotFound(err) {
			return existingConfig, []error{err}
		}

		// Leave missing Authentication resources to the delegates' normal handling.
		if err == nil && auth.Spec.Type == configv1.AuthenticationTypeOIDC {
			directOIDCConfig, _, err := unstructured.NestedStringSlice(existingConfig, authConfigPath...)
			if err != nil {
				return existingConfig, []error{err}
			}

			if len(directOIDCConfig) > 0 {
				return o.observeDirectOIDCToWebhookMigration(genericListers, recorder, existingConfig, auth)
			}
		}
	}

	// Outside the coordinated migration path, preserve the existing behavior: run
	// both observers so each can configure or clean up the resources it owns.
	externalOIDCConfig, errs := o.externalOIDCObserver(genericListers, recorder, existingConfig)
	webhookConfig, webhookErrs := o.webhookObserver(genericListers, recorder, existingConfig)
	errs = append(errs, webhookErrs...)

	observedConfig := map[string]any{}
	for _, config := range []map[string]any{externalOIDCConfig, webhookConfig} {
		// A delegate may return existingConfig; merging must not mutate it.
		if err := mergo.Merge(&observedConfig, runtime.DeepCopyJSON(config)); err != nil {
			return existingConfig, append(errs, fmt.Errorf("merging authentication configuration: %w", err))
		}
	}

	return observedConfig, errs
}

// observeDirectOIDCToWebhookMigration retains direct OIDC until its replacement
// kubeconfig is valid and synchronized, then publishes the authentication switch.
// The caller prunes the returned configuration to the authentication arguments.
func (o *authenticationObserver) observeDirectOIDCToWebhookMigration(
	genericListers configobserver.Listers,
	recorder events.Recorder,
	existingConfig map[string]any,
	auth *configv1.Authentication,
) (map[string]any, []error) {
	// Older CAO versions explicitly referenced the managed Secret. Accept that
	// reference, but do not migrate to a different webhook authenticator.
	if webhook := auth.Spec.WebhookTokenAuthenticator; webhook != nil && webhook.KubeConfig.Name != defaultWebhookSecretName {
		return existingConfig, []error{fmt.Errorf("cannot migrate direct OIDC to webhook: spec.webhookTokenAuthenticator.kubeConfig.name must be %q when set, got %q", defaultWebhookSecretName, webhook.KubeConfig.Name)}
	}

	webhookSecretName := defaultWebhookSecretName
	listers := genericListers.(configobservation.Listers)
	kubeconfigSecret, err := listers.ConfigSecretLister().Secrets(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(webhookSecretName)
	if err != nil {
		return existingConfig, []error{fmt.Errorf("failed to get secret %s/%s: %w", operatorclient.GlobalUserSpecifiedConfigNamespace, webhookSecretName, err)}
	}
	if secretErrors := validateKubeconfigSecret(kubeconfigSecret); len(secretErrors) > 0 {
		return existingConfig, []error{fmt.Errorf("secret %s/%s is invalid: %w", operatorclient.GlobalUserSpecifiedConfigNamespace, webhookSecretName, utilerrors.NewAggregate(secretErrors))}
	}

	destination := resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "webhook-authenticator"}
	source := resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: webhookSecretName}
	// SyncSecret queues an asynchronous copy. Keep requesting it while waiting
	// for the destination informer to observe the validated source kubeconfig.
	if err := genericListers.ResourceSyncer().SyncSecret(destination, source); err != nil {
		return existingConfig, []error{fmt.Errorf("syncing webhook-authenticator secret: %w", err)}
	}

	destinationSecret, err := listers.SecretLister().Secrets(destination.Namespace).Get(destination.Name)
	if errors.IsNotFound(err) {
		return existingConfig, nil
	}
	if err != nil {
		return existingConfig, []error{fmt.Errorf("failed to get secret %s/%s: %w", destination.Namespace, destination.Name, err)}
	}
	if secretErrors := validateKubeconfigSecret(destinationSecret); len(secretErrors) > 0 {
		return existingConfig, []error{fmt.Errorf("secret %s/%s is invalid: %w", destination.Namespace, destination.Name, utilerrors.NewAggregate(secretErrors))}
	}
	if !bytes.Equal(destinationSecret.Data["kubeConfig"], kubeconfigSecret.Data["kubeConfig"]) {
		return existingConfig, nil
	}

	// TODO (follow-up): Establish end-to-end External OIDC webhook readiness;
	// matching valid kubeconfigs alone do not rule out a retained OAuth-era Secret.

	// Publish both webhook arguments together and omit authentication-config.
	// A fresh map keeps the switch atomic without mutating the existing config.
	observedConfig := map[string]any{}
	if err := unstructured.SetNestedSlice(observedConfig, webhookTokenAuthenticatorFile, webhookTokenAuthenticatorPath...); err != nil {
		return existingConfig, []error{err}
	}
	if err := unstructured.SetNestedSlice(observedConfig, webhookTokenAuthenticatorVersion, webhookTokenAuthenticatorVersionPath...); err != nil {
		return existingConfig, []error{err}
	}
	return observedConfig, nil
}
