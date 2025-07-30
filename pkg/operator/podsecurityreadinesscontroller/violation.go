package podsecurityreadinesscontroller

import (
	"context"

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

// isNamespaceViolating checks if a namespace is ready for Pod Security Admission enforcement.
// It returns true if the namespace is violating the Pod Security Admission policy, along with
// the enforce label it was tested against.
func (c *PodSecurityReadinessController) isNamespaceViolating(ctx context.Context, ns *corev1.Namespace) (bool, string, error) {
	nsApplyConfig, err := applyconfiguration.ExtractNamespace(ns, syncerControllerName)
	if err != nil {
		return false, "", err
	}

	enforceLabel := determineEnforceLabelForNamespace(nsApplyConfig)
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
		return false, "", err
	}

	// If there are warnings, the namespace is violating.
	warnings := c.warningsHandler.PopAll()
	if len(warnings) > 0 {
		return true, enforceLabel, nil
	}

	return false, "", nil
}

func determineEnforceLabelForNamespace(ns *applyconfiguration.NamespaceApplyConfiguration) string {
	if _, ok := ns.Annotations[securityv1.MinimallySufficientPodSecurityStandard]; ok {
		// This should generally exist and will be the only supported method of determining
		// the enforce level going forward - however, we're keeping the label fallback for
		// now to account for any workloads not yet annotated using a new enough version of
		// the syncer, such as during upgrade scenarios.
		return ns.Annotations[securityv1.MinimallySufficientPodSecurityStandard]
	}

	targetLevel := ""
	for label := range alertLabels {
		value, ok := ns.Labels[label]
		if !ok {
			continue
		}

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
