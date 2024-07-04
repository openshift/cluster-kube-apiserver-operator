package podsecurityreadinesscontroller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applyconfiguration "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"
)

var podSecurityAlertLabels = []string{
	psapi.AuditLevelLabel,
	psapi.WarnLevelLabel,
}

func (c *PodSecurityReadinessController) isNamespaceViolating(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	if ns.Labels[psapi.EnforceLevelLabel] != "" {
		// If someone has taken care of the enforce label, we don't need to
		// check for violations. Global Config nor PS-Label-Syncer will modify
		// it.
		return false, nil
	}

	targetLevel := ""
	for _, label := range podSecurityAlertLabels {
		levelStr, ok := ns.Labels[label]
		if !ok {
			continue
		}

		level, err := psapi.ParseLevel(levelStr)
		if err != nil {
			klog.V(4).InfoS("invalid level", "namespace", ns.Name, "level", levelStr)
			continue
		}

		if targetLevel == "" {
			targetLevel = levelStr
			continue
		}

		if psapi.CompareLevels(psapi.Level(targetLevel), level) < 0 {
			targetLevel = levelStr
		}
	}

	if targetLevel == "" {
		// Global Config will set it to "restricted".
		targetLevel = string(psapi.LevelRestricted)
	}

	nsApply := applyconfiguration.Namespace(ns.Name).WithLabels(map[string]string{
		psapi.EnforceLevelLabel: string(targetLevel),
	})

	_, err := c.kubeClient.CoreV1().
		Namespaces().
		Apply(ctx, nsApply, metav1.ApplyOptions{
			DryRun:       []string{metav1.DryRunAll},
			FieldManager: "pod-security-readiness-controller",
		})
	if err != nil {
		return false, err
	}

	// The information we want is in the warnings. It collects violations.
	warnings := c.warningsHandler.PopAll()

	return len(warnings) > 0, nil
}
