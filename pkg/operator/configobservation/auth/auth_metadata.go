package auth

import (
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

const (
	targetNamespaceName   = "openshift-kube-apiserver"
	oauthMetadataFilePath = "/etc/kubernetes/static-pod-resources/configmaps/oauth-metadata/oauthMetadata"
	configNamespace       = "openshift-config"
	managedNamespace      = "openshift-config-managed"
)

var (
	topLevelMetadataFilePath = []string{"authConfig", "oauthMetadataFile"}
)

// ObserveAuthMetadata fills in authConfig.OauthMetadataFile with the path for a configMap referenced by the authentication
// config.
func ObserveAuthMetadata(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, topLevelMetadataFilePath)
	}()

	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	currentMetadataFilePath, _, err := unstructured.NestedString(existingConfig, topLevelMetadataFilePath...)
	if err != nil {
		errs = append(errs, err)
	}
	if len(currentMetadataFilePath) > 0 {
		if err := unstructured.SetNestedField(prevObservedConfig, currentMetadataFilePath, topLevelMetadataFilePath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	authConfig, err := listers.AuthConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		recorder.Eventf("ObserveAuthMetadataConfigMap", "authentications.config.openshift.io/cluster: not found")
		klog.Warningf("authentications.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		errs = append(errs, err)
		return prevObservedConfig, errs
	}

	var (
		sourceNamespace string
		sourceConfigMap string
	)

	switch authConfig.Spec.Type {
	case configv1.AuthenticationTypeIntegratedOAuth, "":
		specConfigMap := authConfig.Spec.OAuthMetadata.Name
		statusConfigMap := authConfig.Status.IntegratedOAuthMetadata.Name
		if len(statusConfigMap) == 0 {
			klog.V(5).Infof("no integrated oauth metadata configmap observed from status")
		}

		// Spec configMap takes precedence over Status.
		switch {
		case len(specConfigMap) > 0:
			sourceConfigMap = specConfigMap
			sourceNamespace = configNamespace
		case len(statusConfigMap) > 0:
			sourceConfigMap = statusConfigMap
			sourceNamespace = managedNamespace
		default:
			klog.V(5).Infof("no authentication config metadata specified")
		}

	case configv1.AuthenticationTypeNone:
		// no oauth metadata is served; do not set anything as source
		// in order to delete the configmap and unset oauthMetadataFile

	case configv1.AuthenticationTypeOIDC:
		if _, err := listers.ConfigmapLister_.ConfigMaps(operatorclient.TargetNamespace).Get(AuthConfigCMName); errors.IsNotFound(err) {
			// auth-config does not exist in target namespace yet; do not remove oauth metadata until it's there
			return prevObservedConfig, errs
		} else if err != nil {
			return prevObservedConfig, append(errs, err)
		}

		// no oauth metadata is served; do not set anything as source
		// in order to delete the configmap and unset oauthMetadataFile
	}

	// Sync the user or status-specified configMap to the well-known resting place that corresponds to the oauthMetadataFile path.
	// If neither are set, this updates the destination with an empty source, which deletes the destination resource.
	err = listers.ResourceSyncer().SyncConfigMap(
		resourcesynccontroller.ResourceLocation{
			Namespace: targetNamespaceName,
			Name:      "oauth-metadata",
		},
		resourcesynccontroller.ResourceLocation{
			Namespace: sourceNamespace,
			Name:      sourceConfigMap,
		},
	)
	if err != nil {
		errs = append(errs, err)
		return prevObservedConfig, errs
	}

	// Unsets oauthMetadataFile if we had an empty source.
	if len(sourceConfigMap) == 0 {
		return observedConfig, errs
	}

	// Set oauthMetadataFile.
	if err := unstructured.SetNestedField(observedConfig, oauthMetadataFilePath, topLevelMetadataFilePath...); err != nil {
		recorder.Eventf("ObserveAuthMetadataConfigMap", "Failed setting oauthMetadataFile: %v", err)
		errs = append(errs, err)
	}

	return observedConfig, errs
}
