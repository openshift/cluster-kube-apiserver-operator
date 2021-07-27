package startupmonitorreadiness

import (
	"bytes"
	"encoding/json"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// IsStartupMonitorEnabledFunction returns a function that determines if the startup monitor should be enabled on a cluster
func IsStartupMonitorEnabledFunction(infrastructureLister configlistersv1.InfrastructureLister, operatorClient v1helpers.StaticPodOperatorClient) func() (bool, error) {
	return func() (bool, error) {
		infra, err := infrastructureLister.Get("cluster")
		if err != nil && !apierrors.IsNotFound(err) {
			// we got an error so without the infrastructure object we are not able to determine the type of platform we are running on
			return false, err
		}

		// TODO: uncomment before releasing 4.9
		/*
			if infra.Status.ControlPlaneTopology != configv1.SingleReplicaTopologyMode {
				return false, nil
			}
		*/

		// TODO: remove before releasing 4.9
		_ = infra
		startupMonitorExplicitlyEnabled := false
		operatorSpec, _, _, err := operatorClient.GetOperatorState()
		if err != nil {
			return false, err
		}
		if len(operatorSpec.UnsupportedConfigOverrides.Raw) > 0 {
			observedUnsupportedConfig := map[string]interface{}{}
			if err := json.NewDecoder(bytes.NewBuffer(operatorSpec.UnsupportedConfigOverrides.Raw)).Decode(&observedUnsupportedConfig); err != nil {
				return false, err
			}
			startupMonitorExplicitlyEnabled, _, _ = unstructured.NestedBool(observedUnsupportedConfig, "startupMonitor")
		}
		if !startupMonitorExplicitlyEnabled {
			return false, nil
		}
		// End of TODO
		return true, nil
	}
}
