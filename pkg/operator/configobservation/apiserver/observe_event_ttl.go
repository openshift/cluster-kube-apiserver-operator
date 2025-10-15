package apiserver

import (
	"fmt"

	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var eventTTLPath = []string{"apiServerArguments", "event-ttl"}

// NewObserveEventTTL returns a config observation function that observes
// the EventTTLMinutes field from the KubeAPIServer operator CRD
func NewObserveEventTTL(featureGateAccessor featuregates.FeatureGateAccess) configobserver.ObserveConfigFunc {
	return (&eventTTLObserver{
		featureGateAccessor: featureGateAccessor,
	}).ObserveEventTTL
}

type eventTTLObserver struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

// ObserveEventTTL reads the eventTTLMinutes from the KubeAPIServer operator CRD
func (o *eventTTLObserver) ObserveEventTTL(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config to only include the event-ttl path
		ret = configobserver.Pruned(ret, eventTTLPath)
	}()

	if !o.featureGateAccessor.AreInitialFeatureGatesObserved() {
		// if we haven't observed featuregates yet, return the existing
		return existingConfig, nil
	}

	featureGates, err := o.featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return existingConfig, []error{err}
	}

	if !featureGates.Enabled(features.FeatureEventTTL) {
		// Feature disabled: return no opinion so any previously observed value is removed.
		// Pruning in defer will ensure only the relevant path is considered.
		return map[string]interface{}{}, nil
	}

	kubeAPIServer, err := genericListers.(configobservation.Listers).KubeAPIServerOperatorLister().Get("cluster")
	if err != nil {
		return existingConfig, []error{err}
	}

	// Determine the event TTL value to use
	var eventTTLValue string
	if kubeAPIServer.Spec.EventTTLMinutes > 0 {
		observedConfig := map[string]interface{}{}
		// Use the specified value, convert minutes to duration string (e.g., "180m" for 180 minutes)
		eventTTLValue = fmt.Sprintf("%dm", kubeAPIServer.Spec.EventTTLMinutes)
		if err := unstructured.SetNestedStringSlice(observedConfig, []string{eventTTLValue}, eventTTLPath...); err != nil {
			return existingConfig, []error{err}
		}
		return observedConfig, nil
	}

	// Use default value from the defaultconfig.yaml when EventTTLMinutes is 0 or not set
	return map[string]interface{}{}, nil
}
