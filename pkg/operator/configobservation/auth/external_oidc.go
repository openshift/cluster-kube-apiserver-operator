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

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

const (
	apiServerArgumentsPath = "apiServerArguments"
	argAuthConfig          = "authentication-config"

	SourceAuthConfigCMNamespace = "openshift-config-managed"
	AuthConfigCMName            = "auth-config"
	authConfigKeyName           = "auth-config.json"
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
func (o *externalOIDC) ObserveExternalOIDC(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
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

	if auth.Spec.Type != configv1.AuthenticationTypeOIDC {
		if _, err := listers.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace).Get(AuthConfigCMName); errors.IsNotFound(err) {
			return nil, nil

		} else if err != nil {
			return existingConfig, []error{fmt.Errorf("failed to get configmap %s/%s: %v", operatorclient.TargetNamespace, AuthConfigCMName, err)}
		}

		// empty source name/namespace effectively deletes target configmap
		if err := syncConfigMap(genericListers.ResourceSyncer(), "", "", recorder); err != nil {
			return existingConfig, []error{err}
		}

		return nil, nil
	}

	// auth type is OIDC

	sourceAuthConfig, err := listers.ConfigMapLister().ConfigMaps(SourceAuthConfigCMNamespace).Get(AuthConfigCMName)
	if errors.IsNotFound(err) {
		klog.Warningf("configmap %s/%s not found; skipping configuration of OIDC", SourceAuthConfigCMNamespace, AuthConfigCMName)
		return existingConfig, nil

	} else if err != nil {
		return existingConfig, []error{fmt.Errorf("failed to get configmap %s/%s: %v", SourceAuthConfigCMNamespace, AuthConfigCMName, err)}
	}

	authConfigRaw := sourceAuthConfig.Data[authConfigKeyName]
	if len(authConfigRaw) == 0 {
		return existingConfig, []error{fmt.Errorf("configmap %s/%s is invalid: key '%s' missing or value empty", SourceAuthConfigCMNamespace, AuthConfigCMName, authConfigKeyName)}
	}

	targetAuthConfig, err := listers.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace).Get(AuthConfigCMName)
	if err != nil && !errors.IsNotFound(err) {
		return existingConfig, []error{err}
	}

	if targetAuthConfig == nil || !equality.Semantic.DeepEqual(targetAuthConfig.Data, sourceAuthConfig.Data) {
		if err := syncConfigMap(genericListers.ResourceSyncer(), sourceAuthConfig.Name, sourceAuthConfig.Namespace, recorder); err != nil {
			return existingConfig, []error{err}
		}
	}

	observedConfig := make(map[string]interface{})
	if err := unstructured.SetNestedStringSlice(observedConfig, []string{path.Join("/etc/kubernetes/static-pod-resources/configmaps/", AuthConfigCMName, authConfigKeyName)}, apiServerArgumentsPath, argAuthConfig); err != nil {
		return existingConfig, []error{err}
	}

	return observedConfig, nil
}

func syncConfigMap(syncer resourcesynccontroller.ResourceSyncer, sourceName, sourceNamespace string, recorder events.Recorder) error {
	if err := syncer.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: AuthConfigCMName},
		resourcesynccontroller.ResourceLocation{Namespace: sourceNamespace, Name: sourceName},
	); err != nil {
		return err
	}

	if len(sourceName) == 0 {
		recorder.Eventf("ObserveExternalOIDC", "deleted configmap %s/%s", operatorclient.TargetNamespace, AuthConfigCMName)
	} else {
		recorder.Eventf("ObserveExternalOIDC", "sync requested for configmap %s/%s to %s/%s", sourceNamespace, sourceName, operatorclient.TargetNamespace, AuthConfigCMName)
	}

	return nil
}
