package deadman

import configv1 "github.com/openshift/api/config/v1"

func filteredConditions(conditions []configv1.ClusterOperatorStatusCondition) map[int]configv1.ClusterOperatorStatusCondition {
	filtered := map[int]configv1.ClusterOperatorStatusCondition{}
	for i, condition := range conditions {
		switch condition.Type {
		case configv1.OperatorAvailable, configv1.OperatorDegraded, configv1.OperatorProgressing, configv1.OperatorUpgradeable:
			filtered[i] = condition
		}
	}
	return filtered
}
