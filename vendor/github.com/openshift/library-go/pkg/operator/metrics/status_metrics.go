package metrics

import (
	"github.com/openshift/api/operator/v1"
)

func addStatusAvailableConditionMetrics(c *Collector) {
	c.register(
		"condition_available",
		"Tracks operator available condition",
		[]string{"status"},
		func(spec *v1.OperatorSpec, status *v1.OperatorStatus, staticPodStatus *v1.StaticPodOperatorStatus) (float64, []string, error) {
			var conditions []v1.OperatorCondition
			if status != nil {
				conditions = status.Conditions
			}
			if staticPodStatus != nil {
				conditions = staticPodStatus.Conditions
			}
			for _, c := range conditions {
				if c.Type != v1.OperatorStatusTypeAvailable {
					continue
				}
				switch c.Status {
				case v1.ConditionTrue:
					return 1, []string{string(v1.ConditionTrue)}, nil
				case v1.ConditionFalse:
					return 1, []string{string(v1.ConditionFalse)}, nil
				case v1.ConditionUnknown:
					return 1, []string{string(v1.ConditionUnknown)}, nil
				}
			}
			return 0, []string{}, nil
		})
}

func addStatusFailedConditionMetrics(c *Collector) {
	c.register(
		"condition_failed",
		"Tracks operator failed condition",
		[]string{"status"},
		func(spec *v1.OperatorSpec, status *v1.OperatorStatus, staticPodStatus *v1.StaticPodOperatorStatus) (float64, []string, error) {
			var conditions []v1.OperatorCondition
			if status != nil {
				conditions = status.Conditions
			}
			if staticPodStatus != nil {
				conditions = staticPodStatus.Conditions
			}
			for _, c := range conditions {
				if c.Type != v1.OperatorStatusTypeFailing {
					continue
				}
				switch c.Status {
				case v1.ConditionTrue:
					return 1, []string{string(v1.ConditionTrue)}, nil
				case v1.ConditionFalse:
					return 1, []string{string(v1.ConditionFalse)}, nil
				case v1.ConditionUnknown:
					return 1, []string{string(v1.ConditionUnknown)}, nil
				}
			}
			return 0, []string{}, nil
		})
}

func addStaticPodStatusLatestRevisionMetrics(c *Collector) {
	c.register(
		"static_pod_latest_revision",
		"Tracks static pod operator latest revision",
		[]string{},
		func(spec *v1.OperatorSpec, _ *v1.OperatorStatus, status *v1.StaticPodOperatorStatus) (float64, []string, error) {
			if status == nil {
				return 0, []string{}, nil
			}
			return float64(status.LatestAvailableRevision), []string{}, nil
		})
}

func addStaticPodNodesWithLatestRevisionMetrics(c *Collector) {
	c.register(
		"static_pod_nodes_with_latest_revision", "Tracks number of nodes having latest revision",
		[]string{},
		func(spec *v1.OperatorSpec, _ *v1.OperatorStatus, status *v1.StaticPodOperatorStatus) (float64, []string, error) {
			if status == nil {
				return 0, []string{}, nil
			}
			latestRevision := status.LatestAvailableRevision
			counter := 0
			for _, s := range status.NodeStatuses {
				if s.CurrentRevision == latestRevision {
					counter += 1
				}
			}
			return float64(counter), []string{}, nil
		})
}

func addStaticPodNodesWithFailuresMetrics(c *Collector) {
	c.register(
		"static_pod_failed_nodes",
		"Tracks number of nodes having latest revision",
		[]string{},
		func(spec *v1.OperatorSpec, _ *v1.OperatorStatus, status *v1.StaticPodOperatorStatus) (float64, []string, error) {
			if status == nil {
				return 0, []string{}, nil
			}
			latestRevision := status.LatestAvailableRevision
			counter := 0
			for _, s := range status.NodeStatuses {
				if s.LastFailedRevision == latestRevision {
					counter += 1
				}
			}
			return float64(counter), []string{}, nil
		})
}
