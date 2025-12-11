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

	switch {
	case infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode:
		// reduce the shutdown delay to 0 to reach the maximum downtime for SNO
		observedShutdownDelayDuration = "0s"
	case infra.Spec.PlatformSpec.Type == configv1.AWSPlatformType:
		// AWS has a known issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804
		// We need to extend the shutdown-delay-duration so that an NLB has a chance to notice and remove unhealthy instance.
		// Once the mentioned issue is resolved this code must be removed and default values applied
		//
		// Note this is the official number we got from AWS
		observedShutdownDelayDuration = "129s"
	case infra.Spec.PlatformSpec.Type == configv1.GCPPlatformType:
		// We are receiving inconsistent information from the GCP support team.
		// In some responses, they confirm an additional ~60s delay in traffic propagation,
		// while in others they state that no such delay exists.
		//
		// Regardless of the mixed messaging, we consistently observe late requests in CI.
		// The latest request observed arrived at 67s with the previous 70s timeout.
		//
		// Based on real observations, we update the timeout so that:
		// 0.8 × NEW_LIMIT ≥ 67s.
		//
		// Therefore, the new timeout is set to 95s,
		// which includes an additional 10s safety buffer to account for timing variance
		// and ensure late requests do not cross the 80% threshold.
		//
		// See: https://console.cloud.google.com/support/cases/detail/v2/65801689?project=openshift-gce-devel
		// See: https://issues.redhat.com/browse/OCPBUGS-61674
		observedShutdownDelayDuration = "95s"
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

// ObserveGracefulTerminationDuration sets the graceful termination duration according to the current platform.
func ObserveGracefulTerminationDuration(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
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

	switch {
	case infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode:
		// reduce termination duration from 135s (default) to 15s to reach the maximum downtime for SNO:
		// - the shutdown-delay-duration is set to 0s because there is no load-balancer, and no fallback apiserver
		//   anyway that could benefit from a service network taking out the endpoint gracefully
		// - additional 15s is for in-flight requests
		observedGracefulTerminationDuration = "15"
	case infra.Spec.PlatformSpec.Type == configv1.AWSPlatformType:
		// AWS has a known issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804
		// We need to extend the shutdown-delay-duration so that an NLB has a chance to notice and remove unhealthy instance.
		// Once the mentioned issue is resolved this code must be removed and default values applied
		//
		// 194s is calculated as follows:
		//   the initial 129s is reserved fo the minimal termination period - the time needed for an LB to take an instance out of rotation
		//   additional 60s for finishing all in-flight requests
		//   an extra 5s to make sure the potential SIGTERM will be sent after the server terminates itself
		observedGracefulTerminationDuration = "194"
	case infra.Spec.PlatformSpec.Type == configv1.GCPPlatformType:
		// 160s is calculated as follows:
		//   the initial 95s is reserved fo the minimal termination period - the time needed for an LB to take an instance out of rotation
		//   additional 60s for finishing all in-flight requests
		//   an extra 5s to make sure the potential SIGTERM will be sent after the server terminates itself
		//
		// See: https://console.cloud.google.com/support/cases/detail/v2/65801689?project=openshift-gce-devel
		// See: https://issues.redhat.com/browse/OCPBUGS-61674
		observedGracefulTerminationDuration = "160"
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
