package metrics

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"
)

func NewClient(clientset metricsclient.MetricsV1beta1Interface) *Client {
	return &Client{
		clientset: clientset,
	}
}

type Summary struct {
	// TotalNodes is the total number of nodes reported by the metrics.
	TotalNodes int

	// TotalPods is the total number of pods reported by the metrics.
	TotalPods int
}

func (s *Summary) String() string {
	return fmt.Sprintf("nodes=%d pods=%d", s.TotalNodes, s.TotalPods)
}

type Client struct {
	clientset metricsclient.MetricsV1beta1Interface
}

// GetNodeAndPodSummary returns a summary of Pod and Node metrics.
func (c *Client) GetNodeAndPodSummary(ctx context.Context) (*Summary, error) {
	nodes, err := c.clientset.NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pods, err := c.clientset.PodMetricses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return &Summary{
		TotalNodes: len(nodes.Items),
		TotalPods:  len(pods.Items),
	}, nil
}

type NodeMetricsSummary struct {
	TotalNodes  int
	CPUUsage    resource.Quantity
	MemoryUsage resource.Quantity
}

// SummarizeNodeMetrics goes through each Node metric and sums up the CPU and memory usage.
func SummarizeNodeMetrics(nodes []metricsv1beta1.NodeMetrics) (*NodeMetricsSummary, error) {
	cpuSum := resource.Quantity{}
	memorySum := resource.Quantity{}

	for _, node := range nodes {
		cpuUsage, memoryUsage, err := getQuantity(&node)
		if err != nil {
			return nil, err
		}

		cpuSum.Add(cpuUsage)
		memorySum.Add(memoryUsage)
	}

	metrics := &NodeMetricsSummary{
		TotalNodes:  len(nodes),
		CPUUsage:    cpuSum,
		MemoryUsage: memorySum,
	}

	return metrics, nil
}

func getQuantity(node *metricsv1beta1.NodeMetrics) (cpu resource.Quantity, memory resource.Quantity, err error) {
	cpu, ok := node.Usage[corev1.ResourceCPU]
	if !ok {
		err = fmt.Errorf("missing resource metric %s for node %s", corev1.ResourceCPU, node.GetName())
		return
	}

	memory, ok = node.Usage[corev1.ResourceMemory]
	if !ok {
		err = fmt.Errorf("missing resource metric %s for node %s", corev1.ResourceMemory, node.GetName())
		return
	}

	return
}
