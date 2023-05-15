package probes

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var ProbeExclusionPath = []string{"targetconfigcontroller", "probeExclusion"}

func ObserveProbeExclusions(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	prunedExistingConfig := configobserver.Pruned(existingConfig, ProbeExclusionPath)

	errs := []error{}
	listers := genericListers.(configobservation.Listers)

	infrastructure, err := listers.InfrastructureLister_.Get("cluster")
	if err != nil {
		return prunedExistingConfig, append(errs, err)
	}

	return observeProbeExclusions(infrastructure)
}

func observeProbeExclusions(infrastructure *configv1.Infrastructure) (map[string]interface{}, []error) {
	switch {
	case infrastructure.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode:
		// don't wait for connectivity on single node, since it will never be present.
		const probeExclusion = `exclude=poststarthook/openshift.io-openshift-apiserver-reachable&exclude=poststarthook/openshift.io-oauth-apiserver-reachable`
		observedConfig := map[string]interface{}{}
		if err := unstructured.SetNestedField(observedConfig, ProbeExclusionPath, probeExclusion); err != nil {
			return nil, []error{err}
		}
		return observedConfig, nil

	default:
		// setting the string to just ping allows us to wait for this config observer to be complete before making our first revision
		// I would use empty string here, but our "is this set" logic requires more than empty string and this meaning is fairly clear.
		// this avoids single-node having a slow revision that waits an extra 60s before being ready.
		observedConfig := map[string]interface{}{}
		if err := unstructured.SetNestedField(observedConfig, ProbeExclusionPath, "exclude=ping"); err != nil {
			return nil, []error{err}
		}
		return observedConfig, nil
	}
}
