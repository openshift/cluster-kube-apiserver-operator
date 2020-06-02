package maxinflighttunercontroller

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/metrics"
	"github.com/stretchr/testify/assert"
)

var (
	defaults = getNewMaxInFlightValues("3000", "1000")
	doubled  = getNewMaxInFlightValues("6000", "2000")
)

func TestGetDesiredMaxInFlightValues(t *testing.T) {
	tests := []struct {
		name     string
		defaults MaxInFlightValues
		summary  *metrics.Summary
		expected MaxInFlightValues
	}{
		{
			name:     "WithTotalNodesAndPodsExceedingThreshold",
			defaults: defaults,
			summary: &metrics.Summary{
				TotalNodes: nodeThreshold + 1,
				TotalPods:  podThreshold + 1,
			},
			expected: doubled,
		},
		{
			name:     "WithTotalNodesAndPodsAtThreshold",
			defaults: defaults,
			summary: &metrics.Summary{
				TotalNodes: nodeThreshold,
				TotalPods:  podThreshold,
			},
			expected: doubled,
		},
		{
			name:     "WithTotalNodesAtThreshold",
			defaults: defaults,
			summary: &metrics.Summary{
				TotalNodes: nodeThreshold,
				TotalPods:  podThreshold - 1,
			},
			expected: doubled,
		},
		{
			name:     "WithTotalPodsAtThreshold",
			defaults: defaults,
			summary: &metrics.Summary{
				TotalNodes: nodeThreshold - 1,
				TotalPods:  podThreshold,
			},
			expected: doubled,
		},
		{
			name:     "WithTotalNodesAndPodsBelowThreshold",
			defaults: defaults,
			summary: &metrics.Summary{
				TotalNodes: nodeThreshold - 1,
				TotalPods:  podThreshold - 1,
			},
			expected: defaults,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desired := getDesiredMaxInFlightValues(test.defaults, test.summary)

			assert.Equal(t, test.expected.MaxReadOnlyInFlight, desired.MaxReadOnlyInFlight)
			assert.Equal(t, test.expected.MaxMutatingInFlight, desired.MaxMutatingInFlight)
		})
	}
}

func TestNeedsUpdate(t *testing.T) {
	tests := []struct {
		name     string
		current  MaxInFlightValues
		desired  MaxInFlightValues
		expected bool
	}{
		{
			name:     "WithDesiredExceeding",
			current:  getNewMaxInFlightValues("200", "100"),
			desired:  getNewMaxInFlightValues("201", "101"),
			expected: true,
		},
		{
			name:     "WithDesiredMaxReadOnlyInFlightExceedingOnly",
			current:  getNewMaxInFlightValues("200", "100"),
			desired:  getNewMaxInFlightValues("201", "100"),
			expected: true,
		},
		{
			name:     "WithDesiredMaxMutatingInFlightExceedingOnly",
			current:  getNewMaxInFlightValues("200", "100"),
			desired:  getNewMaxInFlightValues("200", "101"),
			expected: true,
		},
		{
			name:     "WithDesiredNotExceeding",
			current:  getNewMaxInFlightValues("200", "100"),
			desired:  getNewMaxInFlightValues("200", "100"),
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updateNeeded := needsUpdate(test.current, test.desired)
			assert.Equal(t, test.expected, updateNeeded)
		})
	}
}
