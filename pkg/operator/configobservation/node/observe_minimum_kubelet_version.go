package node

import (
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
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
	ret = map[string]interface{}{}
	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, append(errs, err)
	}

	if !featureGates.Enabled(features.FeatureGateMinimumKubeletVersion) {
		klog.Infof("XXXXX disabled 2")
		return existingConfig, nil
	}

	defer func() {
		// Prune the observed config so that it only contains minimumKubeletVersion field.
		ret = configobserver.Pruned(ret, []string{minimumKubeletVersionConfigPath})
	}()

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

	if configNode.Spec.MinimumKubeletVersion == "" {
		// in case minimum kubelet version is not set on cluster
		// return empty set of configs, this helps to unset the config
		// values related to the minimumKubeletVersion.
		// Also, ensures that this observer doesn't break cluster upgrades/downgrades
		klog.Infof("XXXXX off 2")
		return ret, errs
	}

	if err := unstructured.SetNestedField(ret, configNode.Spec.MinimumKubeletVersion, minimumKubeletVersionConfigPath); err != nil {
		return ret, append(errs, err)
	}
	klog.Infof("XXXXX set %s", configNode.Spec.MinimumKubeletVersion)

	return ret, errs
}

type authorizationModeObserver struct {
	featureGateAccessor featuregates.FeatureGateAccess
	authModes           []string
}

func NewAuthorizationModeObserver(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	defaultConfig, err := bindata.UnstructuredDefaultConfig()
	if err != nil {
		// programming error, the built-in configuration should always be valid
		panic(err)
	}

	return (&authorizationModeObserver{
		featureGateAccessor: featureGateAccessor,
		authModes:           AuthModesFromUnstructured(defaultConfig),
	}).ObserveAuthorizationMode
}

func AuthModesFromUnstructured(config map[string]any) []string {
	authModes, found, err := unstructured.NestedStringSlice(config, authModePath...)
	if !found || err != nil {
		return []string{}
	}
	return authModes
}

// ObserveAuthorizationMode watches the featuregate configuration and generates the apiServerArguments.authorization-mode
// It currently hardcodes the default set and adds MinimumKubeletVersion if the feature is set to on.
func (o *authorizationModeObserver) ObserveAuthorizationMode(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	klog.Infof("XXXXX auth mode called")
	ret = map[string]interface{}{}
	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		klog.Infof("XXXXX not initialized")
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		klog.Infof("XXXXX gates nil")
		return existingConfig, append(errs, err)
	}

	defer func() {
		// Prune the observed config so that it only contains minimumKubeletVersion field.
		ret = configobserver.Pruned(ret, authModePath)
	}()

	if err := SetAPIServerArgumentsToEnforceMinimumKubeletVersion(o.authModes, ret, featureGates.Enabled(features.FeatureGateMinimumKubeletVersion)); err != nil {
		klog.Infof("XXXXX failed")
		return existingConfig, append(errs, err)
	}
	klog.Infof("XXXXX success")
	return ret, nil
}

// SetAPIServerArgumentsToEnforceMinimumKubeletVersion modifies the passed in config
// to add the "authorization-mode": "MinimumKubeletVersion" if the feature is on. If it's off, it
// removes it instead.
// This function assumes MinimumKubeletVersion auth mode isn't present by default,
// and should likely be removed when it is.
func SetAPIServerArgumentsToEnforceMinimumKubeletVersion(defaultAuthModes []string, newConfig map[string]interface{}, on bool) error {
	if on {
		defaultAuthModes = append(defaultAuthModes, ModeMinimumKubeletVersion)
	}
	sort.Sort(sort.StringSlice(defaultAuthModes))

	unstructured.RemoveNestedField(newConfig, authModePath...)
	return unstructured.SetNestedStringSlice(newConfig, defaultAuthModes, authModePath...)
}
