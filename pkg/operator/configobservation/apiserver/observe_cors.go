package apiserver

import (
	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var clusterDefaultCORSALlowedOrigins = []string{
	`//127\.0\.0\.1(:|$)`,
	`//localhost(:|$)`,
}

func ObserveAdditionalCORSAllowedOrigins(genericListers configobserver.Listers, recorder events.Recorder, completeExistingConfig map[string]interface{}) (map[string]interface{}, []error) {
	const corsAllowedOriginsPath = "corsAllowedOrigins"
	existingConfig := configobservation.SelectedPaths(completeExistingConfig,
		[]string{corsAllowedOriginsPath},
	)

	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	defaultConfig := map[string]interface{}{}
	if err := unstructured.SetNestedStringSlice(defaultConfig, clusterDefaultCORSALlowedOrigins, corsAllowedOriginsPath); err != nil {
		// this should not happen
		return defaultConfig, append(errs, err)
	}

	// grab the current CORS origins to later check whether they were updated
	currentCORSAllowedOrigins, _, err := unstructured.NestedStringSlice(existingConfig, corsAllowedOriginsPath)
	if err != nil {
		return defaultConfig, append(errs, err)
	}
	currentCORSSet := sets.NewString(currentCORSAllowedOrigins...)
	currentCORSSet.Insert(clusterDefaultCORSALlowedOrigins...)

	observedConfig := map[string]interface{}{}
	apiServer, err := listers.APIServerLister().Get("cluster")
	if errors.IsNotFound(err) {
		klog.Warningf("apiserver.config.openshift.io/cluster: not found")
		return defaultConfig, errs
	}
	if err != nil {
		return existingConfig, errs
	}

	newCORSSet := sets.NewString(clusterDefaultCORSALlowedOrigins...)
	newCORSSet.Insert(apiServer.Spec.AdditionalCORSAllowedOrigins...)
	if err := unstructured.SetNestedStringSlice(observedConfig, newCORSSet.List(), corsAllowedOriginsPath); err != nil {
		errs = append(errs, err)
	}

	if !currentCORSSet.Equal(newCORSSet) {
		recorder.Eventf("ObserveAdditionalCORSAllowedOrigins", "corsAllowedOrigins changed to %q", newCORSSet.List())
	}

	return observedConfig, errs
}
