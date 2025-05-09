package podsecurityreadinesscontroller

import (
	"context"
	"fmt"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applyconfiguration "k8s.io/client-go/applyconfigurations/core/v1"
	psapi "k8s.io/pod-security-admission/api"
)

const (
	syncerControllerName = "pod-security-admission-label-synchronization-controller"
)

func (c *PodSecurityReadinessController) isNamespaceViolating(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	nsApplyConfig, err := applyconfiguration.ExtractNamespace(ns, syncerControllerName)
	if err != nil {
		return false, err
	}

	enforceLabel, ok := nsApplyConfig.Annotations[securityv1.MinimallySufficientPodSecurityStandard]
	if !ok {
		return false, fmt.Errorf("unable to determine if the namespace is violating because the MinimallySufficientPodSecurityStandard annotation wasn't found")
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
