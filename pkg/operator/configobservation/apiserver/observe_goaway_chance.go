package apiserver

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var goawayChancePath = []string{"apiServerArguments", "goaway-chance"}

// ObserveGoawayChance ensures that goaway-chance is 0 for SNO topology
func ObserveGoawayChance(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains apiServerArguments field.
		ret = configobserver.Pruned(ret, goawayChancePath)
	}()

	// read the observed value
	listers := genericListers.(configobservation.Listers)
	infra, err := listers.InfrastructureLister().Get("cluster")
	if err != nil {
		// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
		if apierrors.IsNotFound(err) {
			klog.Warningf("ObserveGoawayChance: infras.%s/cluster not found", configv1.GroupName)
		} else {
			errs = append(errs, err)
		}
		return existingConfig, errs
	}

	observedGoawayChance := "0.001"
	if infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
		// for SNO we want to set goaway-chance to 0
		observedGoawayChance = "0"
	}

	// read the current value
	var currentGoawayChance string
	currentGoawayChanceSlice, _, err := unstructured.NestedStringSlice(existingConfig, goawayChancePath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract goaway chance setting from the existing config: %v", err))
		// keep going, we are only interested in the observed value which will overwrite the current configuration anyway
	}
	if len(currentGoawayChanceSlice) > 0 {
		currentGoawayChance = currentGoawayChanceSlice[0]
	}

	// see if the current and the observed value differ
	observedConfig := map[string]interface{}{}
	if observedGoawayChance != currentGoawayChance {
		if err = unstructured.SetNestedStringSlice(observedConfig, []string{observedGoawayChance}, goawayChancePath...); err != nil {
			return existingConfig, append(errs, err)
		}
		return observedConfig, errs
	}

	// nothing has changed return the original configuration
	return existingConfig, errs
}
