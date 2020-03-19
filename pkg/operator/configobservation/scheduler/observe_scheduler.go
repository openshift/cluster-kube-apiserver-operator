package scheduler

import (
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/klog"
)

// ObserveDefaultNodeSelector reads the defaultNodeSelector from the scheduler configuration instance cluster
func ObserveDefaultNodeSelector(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, _ []error) {
	defaultNodeSelectorPath := []string{"projectConfig", "defaultNodeSelector"}
	defer func() {
		ret = configobserver.Pruned(ret, defaultNodeSelectorPath)
	}()

	listers := genericListers.(configobservation.Listers)
	var errs []error

	observedConfig := map[string]interface{}{}
	schedulerConfig, err := listers.SchedulerLister.Get("cluster")
	if errors.IsNotFound(err) {
		klog.Warningf("scheduler.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return existingConfig, append(errs, err)
	}

	defaultNodeSelector := schedulerConfig.Spec.DefaultNodeSelector
	if len(defaultNodeSelector) > 0 {
		if err := unstructured.SetNestedField(observedConfig, defaultNodeSelector, defaultNodeSelectorPath...); err != nil {
			return existingConfig, append(errs, err)
		}
		currentDefaultNodeSelector, _, err := unstructured.NestedString(existingConfig, defaultNodeSelectorPath...)
		if err != nil {
			errs = append(errs, err)
			// keep going on read error from existing config
		}
		if defaultNodeSelector != currentDefaultNodeSelector {
			recorder.Eventf("ObserveDefaultNodeSelectorChanged", "default node selector changed to %q", defaultNodeSelector)
		}
	}
	return observedConfig, errs
}
