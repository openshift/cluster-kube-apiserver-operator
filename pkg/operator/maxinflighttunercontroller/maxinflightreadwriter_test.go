package maxinflighttunercontroller

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadReadMaxInFlightValuesFromAsset(t *testing.T) {
	defaultsGot, errGot := ReadMaxInFlightValuesFromAsset()

	assert.NoError(t, errGot)
	assert.Equal(t, 3000, *defaultsGot.MaxReadOnlyInFlight)
	assert.Equal(t, 1000, *defaultsGot.MaxMutatingInFlight)
}

func TestReadMaxInFlightValues(t *testing.T) {
	tests := []struct {
		name       string
		spec       *operatorv1.OperatorSpec
		errWant    error
		valuesWant MaxInFlightValues
	}{
		{
			name:       "WithBothSettingsPresent",
			spec:       withMaxInFlightOnly(t, "1000", "500"),
			valuesWant: getNewMaxInFlightValues("1000", "500"),
		},
		{
			name:       "WithBothSettingsMissing",
			spec:       withMaxInFlightOnly(t, "", ""),
			valuesWant: MaxInFlightValues{},
		},
		{
			name:    "WithInvalidMaxReadOnlyRequestsInFlight",
			spec:    withMaxInFlightOnly(t, "x", "100"),
			errWant: fmt.Errorf("error reading %s from APIServerArguments - %s", MaxReadOnlyRequestsInFlight, "strconv.Atoi: parsing \"x\": invalid syntax"),
		},
		{
			name:    "WithInvalidMaxMutatingRequestsInFlight",
			spec:    withMaxInFlightOnly(t, "100", "x"),
			errWant: fmt.Errorf("error reading %s from APIServerArguments - %s", MaxMutatingRequestsInFlight, "strconv.Atoi: parsing \"x\": invalid syntax"),
		},
		{
			name:       "WithMaxReadOnlyRequestsInFlightOnly",
			spec:       withMaxInFlightOnly(t, "1000", ""),
			valuesWant: getNewMaxInFlightValues("1000", ""),
		},
		{
			name:       "WithMaxMutatingRequestsInFlightOnly",
			spec:       withMaxInFlightOnly(t, "", "1000"),
			valuesWant: getNewMaxInFlightValues("", "1000"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valuesGot, errGot := ReadMaxInFlightValues(test.spec.UnsupportedConfigOverrides.Raw)

			if test.errWant != nil {
				require.EqualError(t, test.errWant, errGot.Error())
				return
			}

			require.NoError(t, errGot)
			require.NotNil(t, valuesGot)

			if test.valuesWant.MaxReadOnlyInFlight == nil {
				assert.Nil(t, valuesGot.MaxReadOnlyInFlight)
			} else {
				assert.Equal(t, test.valuesWant.MaxReadOnlyInFlight, valuesGot.MaxReadOnlyInFlight)
			}

			if test.valuesWant.MaxMutatingInFlight == nil {
				assert.Nil(t, valuesGot.MaxMutatingInFlight)
			} else {
				assert.Equal(t, test.valuesWant.MaxMutatingInFlight, valuesGot.MaxMutatingInFlight)
			}
		})
	}
}

func TestWriteMaxInFlightValues(t *testing.T) {
	tests := []struct {
		name     string
		current  *operatorv1.OperatorSpec
		errWant  error
		desired  MaxInFlightValues
		specWant *operatorv1.OperatorSpec
	}{
		{
			name:     "WithMaxReadOnlyRequestsInFlightDoubled",
			current:  withMaxInFlightOnly(t, "1000", "500"),
			desired:  getNewMaxInFlightValues("2000", ""),
			specWant: withMaxInFlightOnly(t, "2000", "500"),
		},
		{
			name:     "WithMaxMutatingRequestsInFlightDoubled",
			current:  withMaxInFlightOnly(t, "1000", "100"),
			desired:  getNewMaxInFlightValues("", "200"),
			specWant: withMaxInFlightOnly(t, "1000", "200"),
		},
		{
			name:     "WithBothMaxRequestsInFlightDoubled",
			current:  withMaxInFlightOnly(t, "1000", "500"),
			desired:  getNewMaxInFlightValues("2000", "1000"),
			specWant: withMaxInFlightOnly(t, "2000", "1000"),
		},
		{
			name:     "WithPreservingOtherArguments",
			current:  withMaxInFlightAndOtherArguments(t, "1000", "500"),
			desired:  getNewMaxInFlightValues("2000", "1000"),
			specWant: withMaxInFlightAndOtherArguments(t, "2000", "1000"),
		},
		{
			name:     "WithUnsupportedConfigOverridesNil",
			current:  &operatorv1.OperatorSpec{},
			desired:  getNewMaxInFlightValues("2000", "1000"),
			specWant: withMaxInFlightOnly(t, "2000", "1000"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errGot := WriteMaxInFlightValues(test.current, test.desired)
			if test.errWant != nil {
				require.EqualError(t, test.errWant, errGot.Error())
				return
			}

			want := &kubecontrolplanev1.KubeAPIServerConfig{}
			err := json.Unmarshal(test.specWant.UnsupportedConfigOverrides.Raw, want)
			require.NoError(t, err)

			got := &kubecontrolplanev1.KubeAPIServerConfig{}
			err = json.Unmarshal(test.current.UnsupportedConfigOverrides.Raw, got)
			require.NoError(t, err)

			assert.True(t, equality.Semantic.DeepEqual(want, got))
			assert.True(t, equality.Semantic.DeepEqual(test.specWant, test.current))
		})
	}
}

func withMaxInFlightOnly(t *testing.T, readonly, mutating string) *operatorv1.OperatorSpec {
	return newOperatorSpec(t, applyMaxInFlightToAPIServerArgument(t, readonly, mutating))
}

func withMaxInFlightAndOtherArguments(t *testing.T, readonly, mutating string) *operatorv1.OperatorSpec {
	return newOperatorSpec(t, applyMaxInFlightToAPIServerArgument(t, readonly, mutating), applyOtherValuesToKubeAPIServerConfig)
}

type initializerFunc func(t *testing.T, overrides UnsupportedOverridesRawType)

func newOperatorSpec(t *testing.T, initializers ...initializerFunc) *operatorv1.OperatorSpec {
	overrides := map[string]json.RawMessage{}

	for _, initializer := range initializers {
		initializer(t, overrides)
	}

	raw, err := json.Marshal(overrides)
	require.NoError(t, err)

	spec := &operatorv1.OperatorSpec{}
	spec.UnsupportedConfigOverrides.Raw = raw
	return spec
}

func applyOtherValuesToKubeAPIServerConfig(t *testing.T, overrides UnsupportedOverridesRawType) {
	arguments := readAPIServerArguments(t, overrides)
	arguments["foo"] = []string{
		"foo_value",
	}
	arguments["bar"] = []string{
		"bar_value",
	}
	writeAPIServerArguments(t, overrides, arguments)

	bytes, err := json.Marshal("http://dummy.url")
	require.NoError(t, err)
	overrides["consolePublicURL"] = bytes
}

func applyMaxInFlightToAPIServerArgument(t *testing.T, readonly, mutating string) initializerFunc {
	return func(t *testing.T, overrides UnsupportedOverridesRawType) {
		values := map[string]kubecontrolplanev1.Arguments{}
		if readonly != "" {
			values[MaxReadOnlyRequestsInFlight] = []string{
				readonly,
			}
		}
		if mutating != "" {
			values[MaxMutatingRequestsInFlight] = []string{
				mutating,
			}
		}

		if len(values) == 0 {
			return
		}

		arguments := readAPIServerArguments(t, overrides)
		for k, v := range values {
			arguments[k] = v
		}

		writeAPIServerArguments(t, overrides, arguments)
	}
}

func readAPIServerArguments(t *testing.T, overrides UnsupportedOverridesRawType) APIServerArgumentType {
	arguments := map[string]kubecontrolplanev1.Arguments{}

	raw := overrides["apiServerArguments"]
	if len(raw) != 0 {
		err := json.Unmarshal(raw, &arguments)
		require.NoError(t, err)
	}

	return arguments
}

func writeAPIServerArguments(t *testing.T, overrides UnsupportedOverridesRawType, arguments APIServerArgumentType) {
	bytes, err := json.Marshal(arguments)
	require.NoError(t, err)

	overrides["apiServerArguments"] = bytes
}

func getMaxInFlightValue(value string) *int {
	if value == "" {
		return nil
	}

	v, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}

	return &v
}

func getNewMaxInFlightValues(readonly, mutating string) MaxInFlightValues {
	return MaxInFlightValues{
		MaxReadOnlyInFlight: getMaxInFlightValue(readonly),
		MaxMutatingInFlight: getMaxInFlightValue(mutating),
	}
}
