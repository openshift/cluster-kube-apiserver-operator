package featuregates

import (
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var configPath = []string{"apiServerArguments", "feature-gates"}

// ObserveFeatureFlags fills in --feature-flags for the kube-apiserver
// TODO make this actually filter out only the featuregates that apply to an individual binary
func ObserveFeatureFlags(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	currentConfigValue, _, err := unstructured.NestedStringSlice(existingConfig, configPath...)
	if err != nil {
		errs = append(errs, err)
	}
	if len(currentConfigValue) > 0 {
		if err := unstructured.SetNestedStringSlice(prevObservedConfig, currentConfigValue, configPath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	configResource, err := listers.FeatureGateLister.Get("cluster")
	// if we have no featuregate, then the installer and MCO probably still have way to reconcile certain custom resources
	// we will assume that this means the same as default and hope for the best
	if apierrors.IsNotFound(err) {
		configResource = &configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureSet: configv1.Default,
			},
		}
	} else if err != nil {
		errs = append(errs, err)
		return prevObservedConfig, errs
	}

	var newConfigValue []string
	if featureSet, ok := configv1.FeatureSets[configResource.Spec.FeatureSet]; ok {
		for _, enable := range featureSet.Enabled {
			newConfigValue = append(newConfigValue, enable+"=true")
		}
		for _, disable := range featureSet.Disabled {
			newConfigValue = append(newConfigValue, disable+"=false")
		}
	} else {
		errs = append(errs, fmt.Errorf(".spec.featureSet %q not found", featureSet))
		return prevObservedConfig, errs
	}
	if !reflect.DeepEqual(currentConfigValue, newConfigValue) {
		recorder.Eventf("ObserveFeatureFlagsUpdated", "Updated %v to %s", strings.Join(configPath, "."), strings.Join(newConfigValue, ","))
	}

	if err := unstructured.SetNestedStringSlice(observedConfig, newConfigValue, configPath...); err != nil {
		recorder.Warningf("ObserveFeatureFlags", "Failed setting %v: %v", strings.Join(configPath, "."), err)
		errs = append(errs, err)
	}

	return observedConfig, errs
}
