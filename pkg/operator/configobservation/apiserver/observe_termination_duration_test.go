package apiserver

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveWatchTerminationDuration(t *testing.T) {
	scenarios := []struct {
		name                    string
		validateKubeAPIConfigFn func(kubecontrolplanev1.KubeAPIServerConfig) error
		existingKubeAPIConfig   map[string]interface{}
		expectedKubeAPIConfig   map[string]interface{}
		platformType            configv1.PlatformType
		controlPlaneTopology    configv1.TopologyMode
	}{

		// scenario 1
		{
			name:                  "default value is not applied",
			expectedKubeAPIConfig: map[string]interface{}{},
		},

		// scenario 2
		{
			name:                  "happy path: a config with some watchTerminationDuration value already exists",
			existingKubeAPIConfig: map[string]interface{}{"gracefulTerminationDuration": "135"},
			expectedKubeAPIConfig: map[string]interface{}{}, // this is okay, the desired state in that case is no data, eventually all the configurations will be merged
		},

		// scenario 3
		{
			name:                  "the shutdown-delay-duration is extended due to a known AWS issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804a",
			existingKubeAPIConfig: map[string]interface{}{"gracefulTerminationDuration": "135"},
			expectedKubeAPIConfig: map[string]interface{}{"gracefulTerminationDuration": "275"},
			platformType:          configv1.AWSPlatformType,
		},

		// scenario 4
		{
			name:                  "sno: shutdown-delay-duration reduced to 0s",
			expectedKubeAPIConfig: map[string]interface{}{"gracefulTerminationDuration": "60"},
			controlPlaneTopology:  configv1.SingleReplicaTopologyMode,
		},

		// scenario 4
		{
			name:                  "sno takes precedence over platform type",
			expectedKubeAPIConfig: map[string]interface{}{"gracefulTerminationDuration": "60"},
			controlPlaneTopology:  configv1.SingleReplicaTopologyMode,
			platformType:          configv1.AWSPlatformType,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("")
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			infrastructureIndexer.Add(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.InfrastructureSpec{PlatformSpec: configv1.PlatformSpec{Type: scenario.platformType}},
				Status:     configv1.InfrastructureStatus{ControlPlaneTopology: scenario.controlPlaneTopology},
			})
			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infrastructureIndexer),
			}

			// act
			observedKubeAPIConfig, err := ObserveWatchTerminationDuration(listers, eventRecorder, scenario.existingKubeAPIConfig)

			// validate
			if len(err) > 0 {
				t.Fatal(err)
			}
			if !cmp.Equal(scenario.expectedKubeAPIConfig, observedKubeAPIConfig) {
				t.Fatalf("unexpected configuraiton, diff = %v", cmp.Diff(scenario.expectedKubeAPIConfig, observedKubeAPIConfig))
			}
		})
	}
}

func TestObserveShutdownDelayDuration(t *testing.T) {
	scenarios := []struct {
		name                    string
		validateKubeAPIConfigFn func(kubecontrolplanev1.KubeAPIServerConfig) error
		existingConfig          kubecontrolplanev1.KubeAPIServerConfig
		platformType            configv1.PlatformType
		controlPlaneTopology    configv1.TopologyMode
	}{

		// scenario 1
		{
			name: "a config with a shutdown-delay-duration value is respected",
			validateKubeAPIConfigFn: func(actualKasConfig kubecontrolplanev1.KubeAPIServerConfig) error {
				if actualKasConfig.APIServerArguments != nil {
					return fmt.Errorf("expected to receive an empty APIServerArguments list, saw %d items in the list", len(actualKasConfig.APIServerArguments))
				}
				// this is okay, the desired state in that case is no data, eventually all the configurations will be merged
				return nil
			},
			existingConfig: kubecontrolplanev1.KubeAPIServerConfig{APIServerArguments: map[string]kubecontrolplanev1.Arguments{"shutdown-delay-duration": {"70s"}}},
		},

		// scenario 2
		{
			name: "the shutdown-delay-duration is extended due to a known AWS issue: https://bugzilla.redhat.com/show_bug.cgi?id=1943804a",
			validateKubeAPIConfigFn: func(actualKasConfig kubecontrolplanev1.KubeAPIServerConfig) error {
				shutdownDurationArgs := actualKasConfig.APIServerArguments["shutdown-delay-duration"]
				if len(shutdownDurationArgs) != 1 {
					return fmt.Errorf("expected only one argument under shutdown-delay-duration key, got %d", len(shutdownDurationArgs))
				}
				if shutdownDurationArgs[0] != "210s" {
					return fmt.Errorf("incorrect shutdown-delay-duration value, expected = 210s, got %v", shutdownDurationArgs[0])
				}
				return nil
			},
			existingConfig: kubecontrolplanev1.KubeAPIServerConfig{APIServerArguments: map[string]kubecontrolplanev1.Arguments{"shutdown-delay-duration": {"70s"}}},
			platformType:   configv1.AWSPlatformType,
		},

		// scenario 3
		{
			name: "happy path: a config without shutdown-delay-duration value is respected",
			validateKubeAPIConfigFn: func(actualKasConfig kubecontrolplanev1.KubeAPIServerConfig) error {
				shutdownDurationArgs := actualKasConfig.APIServerArguments["shutdown-delay-duration"]
				if len(shutdownDurationArgs) > 0 {
					return fmt.Errorf("didn't expect to find a value for shutdown-delay-duration key, got %d", len(shutdownDurationArgs))
				}
				return nil
			},
		},

		// scenario 4
		{
			name: "sno: shutdown-delay-duration reduced to 0s",
			validateKubeAPIConfigFn: func(actualKasConfig kubecontrolplanev1.KubeAPIServerConfig) error {
				shutdownDurationArgs := actualKasConfig.APIServerArguments["shutdown-delay-duration"]
				if len(shutdownDurationArgs) != 1 {
					return fmt.Errorf("expected only one argument under shutdown-delay-duration key, got %d", len(shutdownDurationArgs))
				}
				if shutdownDurationArgs[0] != "0s" {
					return fmt.Errorf("incorrect shutdown-delay-duration value, expected = 0s, got %v", shutdownDurationArgs[0])
				}
				return nil
			},
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
		},

		// scenario 5
		{
			name: "sno takes precedence over platform type",
			validateKubeAPIConfigFn: func(actualKasConfig kubecontrolplanev1.KubeAPIServerConfig) error {
				shutdownDurationArgs := actualKasConfig.APIServerArguments["shutdown-delay-duration"]
				if len(shutdownDurationArgs) != 1 {
					return fmt.Errorf("expected only one argument under shutdown-delay-duration key, got %d", len(shutdownDurationArgs))
				}
				if shutdownDurationArgs[0] != "0s" {
					return fmt.Errorf("incorrect shutdown-delay-duration value, expected = 0s, got %v", shutdownDurationArgs[0])
				}
				return nil
			},
			controlPlaneTopology: configv1.SingleReplicaTopologyMode,
			platformType:         configv1.AWSPlatformType,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			eventRecorder := events.NewInMemoryRecorder("")
			infrastructureIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			infrastructureIndexer.Add(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.InfrastructureSpec{PlatformSpec: configv1.PlatformSpec{Type: scenario.platformType}},
				Status:     configv1.InfrastructureStatus{ControlPlaneTopology: scenario.controlPlaneTopology},
			})
			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infrastructureIndexer),
			}

			// act
			observedKubeAPIConfig, err := ObserveShutdownDelayDuration(listers, eventRecorder, unstructuredAPIConfig(t, scenario.existingConfig))

			// validate
			if len(err) > 0 {
				t.Fatal(err)
			}

			actualKubeAPIServerConfig := kubeAPIServerConfig(t, observedKubeAPIConfig)
			if err := scenario.validateKubeAPIConfigFn(actualKubeAPIServerConfig); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func unstructuredAPIConfig(t *testing.T, existingCfg kubecontrolplanev1.KubeAPIServerConfig) map[string]interface{} {
	existingCfg.TypeMeta = metav1.TypeMeta{
		Kind: "KubeAPIServerConfig",
	}
	marshalledConfig, err := json.Marshal(existingCfg)
	require.NoError(t, err)
	unstructuredObj := &unstructured.Unstructured{}
	require.NoError(t, json.Unmarshal(marshalledConfig, unstructuredObj))
	return unstructuredObj.Object
}

func kubeAPIServerConfig(t *testing.T, observedConfig map[string]interface{}) kubecontrolplanev1.KubeAPIServerConfig {
	unstructuredConfig := unstructured.Unstructured{
		Object: observedConfig,
	}
	jsonConfig, err := unstructuredConfig.MarshalJSON()
	require.NoError(t, err)
	unmarshalledConfig := &kubecontrolplanev1.KubeAPIServerConfig{
		TypeMeta: metav1.TypeMeta{
			Kind: "KubeAPIServerConfig",
		},
	}
	require.NoError(t, json.Unmarshal(jsonConfig, unmarshalledConfig))
	return *unmarshalledConfig
}
