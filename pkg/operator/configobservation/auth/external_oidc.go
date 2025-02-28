package auth

import (
	"fmt"
	"path"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

const (
	SourceAuthConfigCMNamespace = "openshift-config-managed"
	AuthConfigCMName            = "auth-config"
	authConfigKeyName           = "auth-config.json"
)

var (
	authConfigPath = []string{"apiServerArguments", "authentication-config"}
)

func NewObserveExternalOIDC(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&externalOIDC{
		featureGateAccessor: featureGateAccessor,
	}).ObserveExternalOIDC
}

type externalOIDC struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

// ObserveExternalOIDC observes the authentication.config/cluster resource
// and if the type field is set to OIDC, it configures an external OIDC provider
// to the KAS pods by setting the --authentication-config apiserver argument. It also
// takes care of synchronizing the structured auth config file into the apiserver's namespace
// so that it gets mounted as a static file on each node.
func (o *externalOIDC) ObserveExternalOIDC(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, authConfigPath)
	}()

	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		// if we haven't observed featuregates yet, return the existing
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, []error{err}
	}

	if !featureGates.Enabled(features.FeatureGateExternalOIDC) {
		return existingConfig, nil
	}

	listers := genericListers.(configobservation.Listers)
	auth, err := listers.AuthConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		recorder.Eventf("ObserveExternalOIDC", "authentications.config.openshift.io/cluster: not found")
		klog.Warningf("authentications.config.openshift.io/cluster: not found")
		return existingConfig, nil
	} else if err != nil {
		return existingConfig, []error{err}
	}

	targetAuthConfig, err := listers.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace).Get(AuthConfigCMName)
	if err != nil && !errors.IsNotFound(err) {
		return existingConfig, []error{err}
	}

	if auth.Spec.Type != configv1.AuthenticationTypeOIDC {
		// empty source name/namespace effectively deletes target configmap
		if err := genericListers.ResourceSyncer().SyncConfigMap(
			resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: AuthConfigCMName},
			resourcesynccontroller.ResourceLocation{Namespace: "", Name: ""},
		); err != nil {
			return existingConfig, []error{err}
		}

		if targetAuthConfig != nil {
			recorder.Eventf("ObserveExternalOIDC", "OIDC auth configmap %s/%s exists; requested deletion", operatorclient.TargetNamespace, AuthConfigCMName)
		}

		return nil, nil
	}

	// auth type is OIDC

	sourceAuthConfig, err := validateSourceConfigMap(listers)
	if err != nil {
		return existingConfig, []error{err}

	} else if sourceAuthConfig == nil {
		klog.Warningf("configmap %s/%s not found; skipping configuration of OIDC", SourceAuthConfigCMNamespace, AuthConfigCMName)
		return existingConfig, nil
	}

	if err := genericListers.ResourceSyncer().SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: AuthConfigCMName},
		resourcesynccontroller.ResourceLocation{Namespace: sourceAuthConfig.Namespace, Name: sourceAuthConfig.Name},
	); err != nil {
		return existingConfig, []error{err}
	}

	if targetAuthConfig == nil {
		recorder.Eventf("ObserveExternalOIDC", "OIDC auth configmap %s/%s does not exist; requested sync", operatorclient.TargetNamespace, AuthConfigCMName)
	}

	observedConfig := make(map[string]interface{})
	if err := unstructured.SetNestedStringSlice(observedConfig, []string{path.Join("/etc/kubernetes/static-pod-resources/configmaps/", AuthConfigCMName, authConfigKeyName)}, authConfigPath...); err != nil {
		return existingConfig, []error{err}
	}

	return observedConfig, nil
}

func validateSourceConfigMap(listers configobservation.Listers) (*corev1.ConfigMap, error) {
	sourceAuthConfig, err := listers.ConfigMapLister().ConfigMaps(SourceAuthConfigCMNamespace).Get(AuthConfigCMName)
	if errors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get configmap %s/%s: %v", SourceAuthConfigCMNamespace, AuthConfigCMName, err)
	}

	if data, found := sourceAuthConfig.Data[authConfigKeyName]; !found {
		return nil, fmt.Errorf("configmap %s/%s is invalid: key '%s' missing", SourceAuthConfigCMNamespace, AuthConfigCMName, authConfigKeyName)

	} else if len(data) == 0 {
		return nil, fmt.Errorf("configmap %s/%s is invalid: key '%s' has empty value", SourceAuthConfigCMNamespace, AuthConfigCMName, authConfigKeyName)
	}

	return sourceAuthConfig, nil
}
