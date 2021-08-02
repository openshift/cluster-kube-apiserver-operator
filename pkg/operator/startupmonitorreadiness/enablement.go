package startupmonitorreadiness

import (
	"bytes"
	"encoding/json"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// IsStartupMonitorEnabledFunction returns a function that determines if the startup monitor should be enabled on a cluster
func IsStartupMonitorEnabledFunction(infrastructureLister configlistersv1.InfrastructureLister, operatorClient v1helpers.StaticPodOperatorClient) func() (bool, error) {
	return func() (bool, error) {
		infra, err := infrastructureLister.Get("cluster")
		if err != nil && !apierrors.IsNotFound(err) {
			// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
			return false, err
		}

		if infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
			return true, nil
		}

		// for development and debugging
		operatorSpec, _, _, err := operatorClient.GetOperatorState()
		if err != nil {
			return false, err
		}
		if len(operatorSpec.UnsupportedConfigOverrides.Raw) > 0 {
			observedUnsupportedConfig := map[string]interface{}{}
			if err := json.NewDecoder(bytes.NewBuffer(operatorSpec.UnsupportedConfigOverrides.Raw)).Decode(&observedUnsupportedConfig); err != nil {
				return false, err
			}
			enabled, found, err := unstructured.NestedBool(observedUnsupportedConfig, "startupMonitor")
			if err == nil && found {
				return enabled, nil
			}
		}

		return false, nil
	}
}
