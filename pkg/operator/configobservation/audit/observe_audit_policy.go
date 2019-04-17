package audit

import (
	gojson "encoding/json"
	"fmt"

	yaml2 "sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

const policyConfigMapName = "audit-policy"

// ObserveAuditPolicy observes the openshift-config/audit-policy ConfigMap and merges it into the kube-apiserver config.
func ObserveAuditPolicy(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	// copy non-empty .auditConfig.policyConfiguration from existingConfig to prevObservedConfig
	policyPath := []string{"auditConfig", "policyConfiguration"}
	existingPolicy, _, err := unstructured.NestedFieldNoCopy(existingConfig, policyPath...)
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}
	if existingPolicy != nil {
		if err := unstructured.SetNestedField(prevObservedConfig, existingPolicy, policyPath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}

	// get optional openshift-config/audit-policy file, fallback to default config (i.e. observed config unset)
	policyConfigMap, err := listers.ConfigmapLister.ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(policyConfigMapName)
	switch {
	case errors.IsNotFound(err):
		// switch back to default policy by leaving observedConfig empty
	case err != nil:
		// we had an error, return what we had before and exit. this really shouldn't happen
		return prevObservedConfig, append(errs, err)
	case len(policyConfigMap.Data["policy.yaml"]) > 0:
		// TODO: maybe verify config here
		var newPolicy interface{}
		if bs, err := yaml2.YAMLToJSON([]byte(policyConfigMap.Data["policy.yaml"])); err != nil {
			return prevObservedConfig, append(errs, fmt.Errorf("invalid policy.yaml file in ConfigMap %s/%s: %v", operatorclient.GlobalUserSpecifiedConfigNamespace, policyConfigMapName, err))
		} else if err := gojson.Unmarshal(bs, &newPolicy); err != nil {
			return prevObservedConfig, append(errs, fmt.Errorf("invalid policy.yaml file in ConfigMap %s/%s: %v", operatorclient.GlobalUserSpecifiedConfigNamespace, policyConfigMapName, err))
		}

		if newPolicy != nil {
			if err := unstructured.SetNestedField(observedConfig, newPolicy, policyPath...); err != nil {
				errs = append(errs, err)
			}
		}
	default:
		// we had an error, return what we had before and exit. this really shouldn't happen
		return prevObservedConfig, append(errs, fmt.Errorf("no policy.yaml file found in ConfigMap %s/%s", operatorclient.GlobalUserSpecifiedConfigNamespace, policyConfigMapName))
	}

	if !equality.Semantic.DeepEqual(existingPolicy, observedConfig) && len(errs) == 0 {
		recorder.Eventf("ObserveAuditPolicy", "auditConfig.policyConfiguration changed to new content of %s/%s ConfigMap", operatorclient.GlobalUserSpecifiedConfigNamespace, policyConfigMapName)
	}

	return observedConfig, errs
}
