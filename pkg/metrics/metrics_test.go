package metrics

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func TestSummarizeNodeMetrics(t *testing.T) {
	tests := []struct {
		name        string
		metrics     []metricsv1beta1.NodeMetrics
		assertFunc  func(t *testing.T, summary *NodeMetricsSummary, errGot error)
		errWant     error
		summaryWant *NodeMetricsSummary
	}{
		{
			name: "WithMultipleNodes",
			metrics: []metricsv1beta1.NodeMetrics{
				{
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1000m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				{
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1000m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
				{
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1000m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			summaryWant: &NodeMetricsSummary{
				TotalNodes:  3,
				CPUUsage:    resource.MustParse("3000m"),
				MemoryUsage: resource.MustParse("3Gi"),
			},
		},
		{
			name: "WithCPUUsageMissing",
			metrics: []metricsv1beta1.NodeMetrics{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "foo",
					},
					Usage: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
			errWant: fmt.Errorf("missing resource metric %s for node %s", corev1.ResourceCPU, "foo"),
		},
		{
			name: "WithMemoryUsageMissing",
			metrics: []metricsv1beta1.NodeMetrics{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "foo",
					},
					Usage: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1000m"),
					},
				},
			},
			errWant: fmt.Errorf("missing resource metric %s for node %s", corev1.ResourceMemory, "foo"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summaryGot, errGot := SummarizeNodeMetrics(test.metrics)

			if test.errWant != nil {
				assert.Error(t, test.errWant, errGot)
				return
			}

			assert.NoError(t, errGot)
			assert.NotNil(t, summaryGot)
			assert.Equal(t, test.summaryWant.TotalNodes, summaryGot.TotalNodes)
			assert.True(t, test.summaryWant.CPUUsage.Equal(summaryGot.CPUUsage))
			assert.True(t, test.summaryWant.MemoryUsage.Equal(summaryGot.MemoryUsage))
		})
	}
}
