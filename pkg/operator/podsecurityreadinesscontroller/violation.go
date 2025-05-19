package podsecurityreadinesscontroller

import (
	"context"
	"fmt"

	securityv1 "github.com/openshift/api/security/v1"
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

	enforceLabel, err := determineEnforceLabelForNamespace(nsApplyConfig)
	if err != nil {
		return false, err
	}

	nsApply := applyconfiguration.Namespace(ns.Name).WithLabels(map[string]string{
		psapi.EnforceLevelLabel: enforceLabel,
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

func determineEnforceLabelForNamespace(ns *applyconfiguration.NamespaceApplyConfiguration) (string, error) {
	if label, ok := ns.Annotations[securityv1.MinimallySufficientPodSecurityStandard]; ok {
		// This should generally exist and will be the only supported method of determining
		// the enforce level going forward - however, we're keeping the label fallback for
		// now to account for any workloads not yet annotated using a new enough version of
		// the syncer, such as during upgrade scenarios.
		return label, nil
	}

	viableLabels := map[string]string{}

	for alertLabel := range alertLabels {
		if value, ok := ns.Labels[alertLabel]; ok {
			viableLabels[alertLabel] = value
		}
	}

	if len(viableLabels) == 0 {
		// If there are no labels/annotations managed by the syncer, we can't make a decision.
		return "", fmt.Errorf("unable to determine if the namespace is violating because no appropriate labels or annotations were found")
	}

	return pickStrictest(viableLabels), nil
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
