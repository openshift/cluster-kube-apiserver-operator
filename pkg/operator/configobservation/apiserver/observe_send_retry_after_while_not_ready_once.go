package apiserver

import (
	"fmt"
	"reflect"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

var sendRetryAfterWhileNotReadyOncePath = []string{"apiServerArguments", "send-retry-after-while-not-ready-once"}

// ObserveSendRetryAfterWhileNotReadyOnce ensures that send-retry-after-while-not-ready-once is set for SNO clusters.
func ObserveSendRetryAfterWhileNotReadyOnce(genericListers configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	defer func() {
		// Prune the observed config so that it only contains apiServerArguments field.
		ret = configobserver.Pruned(ret, sendRetryAfterWhileNotReadyOncePath)
	}()

	// read the observed value
	listers := genericListers.(configobservation.Listers)
	infra, err := listers.InfrastructureLister().Get("cluster")
	if err != nil && !apierrors.IsNotFound(err) {
		// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
		return existingConfig, append(errs, err)
	}

	observedSendRetryAfterWhileNotReadyOnce := strconv.FormatBool(infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode)

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
