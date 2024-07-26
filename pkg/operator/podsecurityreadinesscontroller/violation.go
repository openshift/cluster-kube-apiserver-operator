package podsecurityreadinesscontroller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	applyconfiguration "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"
)

const (
	syncerControllerName = "pod-security-admission-label-synchronization-controller"
)

var (
	alertLabels = sets.New(psapi.WarnLevelLabel, psapi.AuditLevelLabel)
)

func (c *PodSecurityReadinessController) isNamespaceViolating(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	nsApplyConfig, err := applyconfiguration.ExtractNamespace(ns, syncerControllerName)
	if err != nil {
		return false, err
	}

	viableLabels := map[string]string{}
	for alertLabel := range alertLabels {
		if value, ok := nsApplyConfig.Labels[alertLabel]; ok {
			viableLabels[alertLabel] = value
		}
	}
	if len(viableLabels) == 0 {
		// If there are no labels managed by the syncer, we can't make a decision.
		return false, nil
	}

	nsApply := applyconfiguration.Namespace(ns.Name).WithLabels(map[string]string{
		psapi.EnforceLevelLabel: pickStrictest(viableLabels),
	})

	_, err = c.kubeClient.CoreV1().
		Namespaces().
		Apply(ctx, nsApply, metav1.ApplyOptions{
			DryRun:       []string{metav1.DryRunAll},
			FieldManager: "pod-security-readiness-controller",
		})
	if err != nil {
		return false, err
	}

	// If there are warnings, the namespace is violating.
	return len(c.warningsHandler.PopAll()) > 0, nil
}

func pickStrictest(viableLabels map[string]string) string {
	targetLevel := ""
	for label, value := range viableLabels {
		level, err := psapi.ParseLevel(value)
		if err != nil {
			klog.V(4).InfoS("invalid level", "label", label, "value", value)
			continue
		}

		if targetLevel == "" {
			targetLevel = value
			continue
		}

		if psapi.CompareLevels(psapi.Level(targetLevel), level) < 0 {
			targetLevel = value
		}
	}

	if targetLevel == "" {
		// Global Config will set it to "restricted", but shouldn't happen.
		return string(psapi.LevelRestricted)
	}

	return targetLevel
}
