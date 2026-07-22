package apiserver

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var sendRetryAfterWhileNotReadyOncePath = []string{"apiServerArguments", "send-retry-after-while-not-ready-once"}

// ObserveSendRetryAfterWhileNotReadyOnce ensures that send-retry-after-while-not-ready-once is set for all
// control plane topologies.
//
// Historically this was enabled only for single-node (SNO) clusters, where the load balancer has a single
// target and inevitably routes requests to a kube-apiserver that is not ready yet. However, it has been
// observed (OCPBUGS-86789) that on multi-node clusters load balancers can also route requests to a
// not-yet-ready kube-apiserver even while healthy replicas are available. Such requests could be processed
// before RBAC and admission post-start hooks have completed. Since no load balancer implementation
// (cloud LB, on-prem haproxy/keepalived, or the in-cluster kubernetes.default service) can be perfectly
// synchronized with /readyz, the only robust protection is server-side: reject early requests with
// Retry-After until the server has been ready once.
func ObserveSendRetryAfterWhileNotReadyOnce(_ configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains apiServerArguments field.
		ret = configobserver.Pruned(ret, sendRetryAfterWhileNotReadyOncePath)
	}()

	// the protection is topology-independent, see the function documentation
	observedSendRetryAfterWhileNotReadyOnce := "true"

	// read the current value
	var currentSendRetryAfterWhileNotReadyOnce string
	currentSendRetryAfterWhileNotReadyOnceSlice, _, err := unstructured.NestedStringSlice(existingConfig, sendRetryAfterWhileNotReadyOncePath...)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to extract send retry after while not ready setting from the existing config: %v", err))
		// keep going, we are only interested in the observed value which will overwrite the current configuration anyway
	}
	if len(currentSendRetryAfterWhileNotReadyOnceSlice) > 0 {
		currentSendRetryAfterWhileNotReadyOnce = currentSendRetryAfterWhileNotReadyOnceSlice[0]
	}

	// see if the current and the observed value differ
	observedConfig := map[string]interface{}{}
	if !reflect.DeepEqual(observedSendRetryAfterWhileNotReadyOnce, currentSendRetryAfterWhileNotReadyOnce) {
		if err = unstructured.SetNestedStringSlice(observedConfig, []string{observedSendRetryAfterWhileNotReadyOnce}, sendRetryAfterWhileNotReadyOncePath...); err != nil {
			return existingConfig, append(errs, err)
		}
		return observedConfig, errs
	}

	// nothing has changed return the original configuration
	return existingConfig, errs
}
