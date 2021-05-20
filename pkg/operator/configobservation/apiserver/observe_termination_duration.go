package apiserver

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var shutdownDelayDurationPath = []string{"apiServerArguments", "shutdown-delay-duration"}

var gracefulTerminationDurationPath = []string{"gracefulTerminationDuration"}

// ObserveShutdownDelayDuration allows for overwriting shutdown-delay-duration value.
// It exists because the time needed for an LB to notice and remove unhealthy instances might vary by platform.
func ObserveShutdownDelayDuration(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains shutdown-delay-duration field.
		ret = configobserver.Pruned(ret, shutdownDelayDurationPath)
	}()

	// read the observed value
	var observedShutdownDelayDuration string
	listers := genericListers.(configobservation.Listers)
	infra, err := listers.InfrastructureLister().Get("cluster")
	if err != nil && !apierrors.IsNotFound(err) {
		// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
		return existingConfig, append(errs, err)
	}
	switch infra.Spec.PlatformSpec.Type {
	case configv1.AWSPlatformType:
		// AWS has a known issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804
		// We need to extend the shutdown-delay-duration so that an NLB has a chance to notice and remove unhealthy instance.
		// Once the mentioned issue is resolved this code must be removed and default values applied
		observedShutdownDelayDuration = "210s"
	default:
		// don't override default value
		return map[string]interface{}{}, errs
	}

	// read the current value
	var currentShutdownDelayDuration string
	currentShutdownDelaySlice, _, err := unstructured.NestedStringSlice(existingConfig, shutdownDelayDurationPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract shutdown delay duration from the existing config: %v", err))
		// keep going, we are only interested in the observed value which will overwrite the current configuration anyway
	}
	if len(currentShutdownDelaySlice) > 0 {
		currentShutdownDelayDuration = currentShutdownDelaySlice[0]
	}

	// see if the current and the observed value differ
	observedConfig := map[string]interface{}{}
	if currentShutdownDelayDuration != observedShutdownDelayDuration {
		if err = unstructured.SetNestedStringSlice(observedConfig, []string{observedShutdownDelayDuration}, shutdownDelayDurationPath...); err != nil {
			return existingConfig, append(errs, err)
		}
		return observedConfig, errs
	}

	// nothing has changed return the original configuration
	return existingConfig, errs
}

// ObserveShutdownDelayDuration sets the graceful termination duration according to the current platform.
func ObserveWatchTerminationDuration(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains gracefulTerminationDuration field.
		ret = configobserver.Pruned(ret, gracefulTerminationDurationPath)
	}()

	// read the observed value
	var observedGracefulTerminationDuration string
	listers := genericListers.(configobservation.Listers)
	infra, err := listers.InfrastructureLister().Get("cluster")
	if err != nil && !apierrors.IsNotFound(err) {
		// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
		return existingConfig, append(errs, err)
	}
	switch infra.Spec.PlatformSpec.Type {
	case configv1.AWSPlatformType:
		// AWS has a known issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804
		// We need to extend the shutdown-delay-duration so that an NLB has a chance to notice and remove unhealthy instance.
		// Once the mentioned issue is resolved this code must be removed and default values applied
		observedGracefulTerminationDuration = "275"
	default:
		// don't override default value
		return map[string]interface{}{}, errs
	}

	// read the current value
	currentGracefulTerminationDuration, _, err := unstructured.NestedString(existingConfig, gracefulTerminationDurationPath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract gracefulTerminationDuration from the existing config: %v, path = %v", err, gracefulTerminationDurationPath))
		// keep going, we are only interested in the observed value which will overwrite the current configuration anyway
	}

	// see if the current and the observed value differ
	observedConfig := map[string]interface{}{}
	if currentGracefulTerminationDuration != observedGracefulTerminationDuration {
		if err = unstructured.SetNestedField(observedConfig, observedGracefulTerminationDuration, gracefulTerminationDurationPath...); err != nil {
			return existingConfig, append(errs, err)
		}
		return observedConfig, errs
	}

	// nothing has changed return the original configuration
	return existingConfig, errs
}
