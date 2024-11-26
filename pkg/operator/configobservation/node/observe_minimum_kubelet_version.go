package node

import (
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

var (
	ModeMinimumKubeletVersion       = "MinimumKubeletVersion"
	minimumKubeletVersionConfigPath = "minimumKubeletVersion"
	authModeFlag                    = "authorization-mode"
	apiServerArgs                   = "apiServerArguments"
	authModePath                    = []string{apiServerArgs, authModeFlag}
	// The default value for apiServerArguments.authorization-mode.
	// Should be synced with bindata/assets/config/defaultconfig.yaml
	DefaultAuthorizationModes = []string{"Scope", "SystemMasters", "RBAC", "Node"}
)

type minimumKubeletVersionObserver struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

func NewMinimumKubeletVersionObserver(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&minimumKubeletVersionObserver{
		featureGateAccessor: featureGateAccessor,
	}).ObserveMinimumKubeletVersion
}

// ObserveKubeletMinimumVersion watches the node configuration and generates the minimumKubeletVersion
func (o *minimumKubeletVersionObserver) ObserveMinimumKubeletVersion(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains minimumKubeletVersion field.
		ret = configobserver.Pruned(ret, []string{minimumKubeletVersionConfigPath})
	}()

	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, append(errs, err)
	}

	if !featureGates.Enabled(features.FeatureGateMinimumKubeletVersion) {
		return existingConfig, nil
	}

	nodeLister := genericListers.(NodeLister)
	configNode, err := nodeLister.NodeLister().Get("cluster")
	// we got an error so without the node object we are not able to determine minimumKubeletVersion
	if err != nil {
		// if config/v1/node/cluster object is not found, that can be treated as a non-error case, but raise a warning
		if apierrors.IsNotFound(err) {
			klog.Warningf("ObserveMinimumKubeletVersion: nodes.%s/cluster not found", configv1.GroupName)
		} else {
			errs = append(errs, err)
		}
		return existingConfig, errs
	}

	ret = map[string]interface{}{}
	if configNode.Spec.MinimumKubeletVersion == "" {
		// in case minimum kubelet version is not set on cluster
		// return empty set of configs, this helps to unset the config
		// values related to the minimumKubeletVersion.
		// Also, ensures that this observer doesn't break cluster upgrades/downgrades
		return ret, errs
	}

	if err := unstructured.SetNestedField(ret, configNode.Spec.MinimumKubeletVersion, minimumKubeletVersionConfigPath); err != nil {
		return existingConfig, append(errs, err)
	}

	return ret, errs
}

type authorizationModeObserver struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

func NewAuthorizationModeObserver(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&authorizationModeObserver{
		featureGateAccessor: featureGateAccessor,
	}).ObserveAuthorizationMode
}

// ObserveAuthorizationMode watches the featuregate configuration and generates the apiServerArguments.authorization-mode
// It currently hardcodes the default set and adds MinimumKubeletVersion if the feature is set to on.
func (o *authorizationModeObserver) ObserveAuthorizationMode(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	ret = map[string]interface{}{}
	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, append(errs, err)
	}

	defer func() {
		// Prune the observed config so that it only contains minimumKubeletVersion field.
		ret = configobserver.Pruned(ret, authModePath)
	}()

	if err := SetAPIServerArgumentsToEnforceMinimumKubeletVersion(ret, featureGates.Enabled(features.FeatureGateMinimumKubeletVersion)); err != nil {
		return existingConfig, append(errs, err)
	}
	return ret, nil
}

// SetAPIServerArgumentsToEnforceMinimumKubeletVersion modifies the passed in config
// to add the "authorization-mode": "MinimumKubeletVersion" if the feature is on. If it's off, it
// removes it instead.
func SetAPIServerArgumentsToEnforceMinimumKubeletVersion(newConfig map[string]interface{}, on bool) error {
	defaultSet := DefaultAuthorizationModes
	if on {
		defaultSet = append(defaultSet, ModeMinimumKubeletVersion)
	}
	sort.Sort(sort.StringSlice(defaultSet))

	unstructured.RemoveNestedField(newConfig, authModePath...)
	return unstructured.SetNestedStringSlice(newConfig, defaultSet, authModePath...)
}
