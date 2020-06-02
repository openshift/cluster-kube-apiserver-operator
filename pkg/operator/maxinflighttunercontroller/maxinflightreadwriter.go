package maxinflighttunercontroller

import (
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"
)

const (
	MaxMutatingRequestsInFlight = "max-mutating-requests-inflight"
	MaxReadOnlyRequestsInFlight = "max-requests-inflight"

	// this is the key under UnsupportedConfigOverrides into which we will
	// add max-inflight values
	APIServerArgumentsKey = "apiServerArguments"
)

type UnsupportedOverridesRawType map[string]json.RawMessage

type APIServerArgumentType map[string]kubecontrolplanev1.Arguments

type MaxInFlightValues struct {
	// MaxReadOnlyInFlight holds the value of 'max-requests-inflight'.
	// A nil means no value has been specified.
	MaxReadOnlyInFlight *int

	// MaxMutatingInFlight holds the value of 'max-mutating-requests-inflight'.
	// A nil means no value has been specified.
	MaxMutatingInFlight *int
}

func (v *MaxInFlightValues) String() string {
	readonly := "<nil>"
	if v.MaxReadOnlyInFlight != nil {
		readonly = strconv.Itoa(*v.MaxReadOnlyInFlight)
	}

	mutating := "<nil>"
	if v.MaxMutatingInFlight != nil {
		mutating = strconv.Itoa(*v.MaxMutatingInFlight)
	}

	return fmt.Sprintf("%s=%s %s=%s", MaxReadOnlyRequestsInFlight, readonly, MaxMutatingRequestsInFlight, mutating)
}

func ReadMaxInFlightValuesFromAsset() (MaxInFlightValues, error) {
	const (
		// This is the asset from which we read the default
		// readonly and mutating max-inflight values.
		AssetName = "v4.1.0/config/defaultconfig.yaml"
	)

	raw, err := v410_00_assets.Asset(AssetName)
	if err != nil {
		return MaxInFlightValues{}, fmt.Errorf("failed to load asset from bindata asset=%s - %s", AssetName, err.Error())
	}

	jsonRaw, err := kyaml.ToJSON(raw)
	if err != nil {
		return MaxInFlightValues{}, fmt.Errorf("failed to convert yaml to json asset=%s - %s", AssetName, err.Error())
	}

	defaults, err := ReadMaxInFlightValues(jsonRaw)
	if err != nil {
		return MaxInFlightValues{}, fmt.Errorf("failed to read default max-inflight values asset=%s - %s", AssetName, err.Error())
	}

	if defaults.MaxMutatingInFlight == nil || defaults.MaxReadOnlyInFlight == nil {
		return MaxInFlightValues{}, fmt.Errorf("default values for both max-inflight arguments must exist asset=%s", AssetName)
	}

	return defaults, nil
}

func ReadMaxInFlightValuesFromConfigMap(cm *corev1.ConfigMap) (MaxInFlightValues, error) {
	const (
		configKey = "config.yaml"
	)

	configRaw, ok := cm.Data[configKey]
	if !ok || len(configRaw) == 0 {
		return MaxInFlightValues{}, fmt.Errorf("faile to read max-inflight values - no '%s' key found configmap=%s", configKey, cm.GetName())
	}

	return ReadMaxInFlightValues([]byte(configRaw))
}

func ReadMaxInFlightValues(raw []byte) (MaxInFlightValues, error) {
	if len(raw) == 0 {
		return MaxInFlightValues{}, nil
	}

	// since we are reading we can safely unmarshal into a KubeAPIServerConfig type.
	config := kubecontrolplanev1.KubeAPIServerConfig{}
	if err := json.Unmarshal(raw, &config); err != nil {
		return MaxInFlightValues{}, err
	}

	readonly, err := readFromAPIServerArguments(config.APIServerArguments, MaxReadOnlyRequestsInFlight)
	if err != nil {
		return MaxInFlightValues{}, fmt.Errorf("error reading %s from APIServerArguments - %s", MaxReadOnlyRequestsInFlight, err.Error())
	}

	mutating, err := readFromAPIServerArguments(config.APIServerArguments, MaxMutatingRequestsInFlight)
	if err != nil {
		return MaxInFlightValues{}, fmt.Errorf("error reading %s from APIServerArguments - %s", MaxMutatingRequestsInFlight, err.Error())
	}

	return MaxInFlightValues{
		MaxReadOnlyInFlight: readonly,
		MaxMutatingInFlight: mutating,
	}, nil
}

func WriteMaxInFlightValues(spec *operatorv1.OperatorSpec, desired MaxInFlightValues) error {
	// we want to retain existing keys/values inside UnsupportedConfigOverrides
	overrides := UnsupportedOverridesRawType{}
	if len(spec.UnsupportedConfigOverrides.Raw) > 0 {
		if err := json.Unmarshal(spec.UnsupportedConfigOverrides.Raw, &overrides); err != nil {
			return fmt.Errorf("failed to unmarshal UnsupportedConfigOverrides - %s", err.Error())
		}
	}

	arguments := APIServerArgumentType{}
	argumentsRaw, ok := overrides[APIServerArgumentsKey]
	if ok && len(argumentsRaw) > 0 {
		bytes, err := argumentsRaw.MarshalJSON()
		if err != nil {
			return fmt.Errorf("failed to retrieve APIServerArguments raw bytes - %s", err.Error())
		}

		if err := json.Unmarshal(bytes, &arguments); err != nil {
			return fmt.Errorf("failed to unmarshal APIServerArguments - %s", err.Error())
		}
	}

	if len(arguments) == 0 {
		arguments = APIServerArgumentType{}
	}

	writeToAPIServerArguments(arguments, MaxReadOnlyRequestsInFlight, desired.MaxReadOnlyInFlight)
	writeToAPIServerArguments(arguments, MaxMutatingRequestsInFlight, desired.MaxMutatingInFlight)

	// now let's write the new APIServerArguments into overrides
	bytes, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("failed to marshal modified APIServerArguments - %s", err.Error())
	}
	overrides[APIServerArgumentsKey] = bytes

	raw, err := json.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("failed to marshal UnsupportedConfigOverrides - %s", err.Error())
	}

	spec.UnsupportedConfigOverrides.Raw = raw
	return nil
}

func writeToAPIServerArguments(arguments APIServerArgumentType, key string, value *int) {
	if value == nil {
		return
	}

	s := strconv.Itoa(*value)
	if len(s) == 0 {
		return
	}
	arguments[key] = []string{s}
}

func readFromAPIServerArguments(config APIServerArgumentType, key string) (*int, error) {
	v, ok := config[key]
	if !ok {
		return nil, nil
	}

	// If the key is specified with an empty array, we are being permissive
	// treat it as if the user has not specified any value.
	if len(v) == 0 {
		return nil, nil
	}

	value, err := strconv.Atoi(v[0])
	if err != nil {
		return nil, err
	}

	return &value, nil
}
