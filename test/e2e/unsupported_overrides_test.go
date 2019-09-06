package e2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/*
				BREAKAGE NOTIFICATION TEST
  The purpose of this test is provde a general notification if and when UnsupportedConfigOverrides
  either become broken and/or are removed all together.  The OpenShift Starter environments utilize
  these configurations and we'd like to be notified when this tests fails.  The following email
  address can be used to alert us of any issues if and when they arrive:
			openshift-cr@redhat.com

  THIS TEST CAN SAFELY BE SKIPPED ONCE A NOTIFICATION HAS BEEN SENT!
*/
func TestUnsupportedConfigOverrides(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	kubeAPIServerOperatorClient := operatorClient.KubeAPIServers()

	testCases := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "admissionPlugins",
			config: map[string]interface{}{
				"apiVersion": "kubecontrolplane.config.openshift.io/v1",
				"kind":       "KubeAPIServerConfig",
				"admission": map[string]interface{}{
					"enabledPlugins": []interface{}{
						"autoscaling.openshift.io/ClusterResourceOverride",
						"autoscaling.openshift.io/RunOnceDuration",
					},
					"pluginConfig": map[string]interface{}{
						"autoscaling.openshift.io/ClusterResourceOverride": map[string]interface{}{
							"configuration": map[string]interface{}{
								"apiVersion":                  "autoscaling.openshift.io/v1",
								"kind":                        "ClusterResourceOverrideConfig",
								"cpuRequestToLimitPercent":    2,
								"limitCPUToMemoryPercent":     200,
								"memoryRequestToLimitPercent": 50,
							},
						},
						"autoscaling.openshift.io/RunOnceDuration": map[string]interface{}{
							"configuration": map[string]interface{}{
								"apiVersion":                 "autoscaling.openshift.io/v1",
								"kind":                       "RunOnceDurationConfig",
								"activeDeadlineSecondsLimit": 3600,
							},
						},
					},
				},
			},
		},
	}

	// kube-apiserver must be available, not progressing, and not failing to continue
	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)

	// Reset the configuration after a successful test
	defer func() {
		_, err := updateAPIServerOperatorConfigSpec(kubeAPIServerOperatorClient, func(operator *operatorv1.KubeAPIServer) {
			operator.Spec.UnsupportedConfigOverrides.Raw = nil
		})
		assert.NoError(t, err)

		// Sleep for 7 minutes.  This should be long enough for a successful roll-out
		time.Sleep(7 * time.Minute)
	}()

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%v", tc.name), func(t *testing.T) {
			updateAPIServerOperatorConfigSpec(kubeAPIServerOperatorClient, func(operator *operatorv1.KubeAPIServer) {
				b, err := json.Marshal(tc.config)
				require.NoError(t, err)
				operator.Spec.UnsupportedConfigOverrides.Raw = b
			})

			// Sleep for 7 minutes.  This should be long enough for a successful roll-out
			time.Sleep(7 * time.Minute)

			// This will fail, after 10 minutes, if the Unsupported Overrides have caused the operator to go degraded
			test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)
		})
	}
}

func updateAPIServerOperatorConfigSpec(client operatorclient.KubeAPIServerInterface, updateFunc func(spec *operatorv1.KubeAPIServer)) (*operatorv1.KubeAPIServer, error) {
	apiServer, err := client.Get("cluster", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		apiServer, err = client.Create(&operatorv1.KubeAPIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}})
	}
	if err != nil {
		return nil, err
	}
	updateFunc(apiServer)
	return client.Update(apiServer)
}
