package podsecurityreadinesscontroller

import (
	"context"
	"fmt"
	"strings"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"
)

func (c *PodSecurityReadinessController) classifyViolatingNamespace(ctx context.Context, conditions *podSecurityOperatorConditions, ns *corev1.Namespace, enforceLevel string) error {
	if runLevelZeroNamespaces.Has(ns.Name) {
		conditions.addViolatingRunLevelZero(ns)
		return nil
	}
	if strings.HasPrefix(ns.Name, "openshift") {
		conditions.addViolatingOpenShift(ns)
		return nil
	}
	if ns.Labels[labelSyncControlLabel] == "false" {
		conditions.addViolatingDisabledSyncer(ns)
		return nil
	}
	klog.InfoS("Checking for user violations", "namespace", ns.Name, "enforceLevel", enforceLevel)
	isUserViolation, err := c.isUserViolation(ctx, ns, enforceLevel)
	if err != nil {
		klog.V(2).ErrorS(err, "Error checking user violations", "namespace", ns.Name)
		// Transient API server error or temporary resource unavailability (most likely).
		// Theoretically, psapi parsing errors could occur that retry without hope for recovery.
		return err
	}
	klog.InfoS("User violation check result", "namespace", ns.Name, "isUserViolation", isUserViolation)
	if isUserViolation {
		klog.InfoS("Adding namespace to user SCC violations", "namespace", ns.Name)
		conditions.addUserSCCViolation(ns)
		return nil
	}

	// Historically, we assume that this is a customer issue, but
	// actually it means we don't know what the root cause is.
	conditions.addViolatingCustomer(ns)

	return nil
}

func (c *PodSecurityReadinessController) isUserViolation(ctx context.Context, ns *corev1.Namespace, label string) (bool, error) {
	// Parse the violating level
	var enforcementLevel psapi.Level
	switch strings.ToLower(label) {
	case "restricted":
		enforcementLevel = psapi.LevelRestricted
	case "baseline":
		enforcementLevel = psapi.LevelBaseline
	case "privileged":
		// If privileged is violating, something is seriously wrong
		// but testing against privileged level is pointless (everything passes)
		klog.V(2).InfoS("Namespace violating privileged level - skipping user check",
			"namespace", ns.Name)
		return false, nil
	default:
		return false, fmt.Errorf("unknown level: %q", label)
	}

	// List all pods and filter for user-annotated ones
	allPods, err := c.kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to list pods in namespace", "namespace", ns.Name)
		return false, err
	}

	// Filter for user-annotated pods
	var userPods []corev1.Pod
	for _, pod := range allPods.Items {
		if pod.Annotations[securityv1.ValidatedSCCSubjectTypeAnnotation] == "user" {
			userPods = append(userPods, pod)
		}
	}

	if len(userPods) == 0 {
		return false, nil // No user pods = violation is from service accounts
	}

	// Test user pods against the violating level
	enforcementVersion := psapi.LatestVersion()
	for _, pod := range userPods {
		klog.InfoS("Evaluating user pod against PSA level",
			"namespace", ns.Name, "pod", pod.Name, "level", label,
			"podSecurityContext", pod.Spec.SecurityContext)
		
		// Log container security contexts for debugging
		for i, container := range pod.Spec.Containers {
			klog.InfoS("Container security context",
				"namespace", ns.Name, "pod", pod.Name, "container", i,
				"securityContext", container.SecurityContext)
		}
		
		results := c.psaEvaluator.EvaluatePod(
			psapi.LevelVersion{Level: enforcementLevel, Version: enforcementVersion},
			&pod.ObjectMeta,
			&pod.Spec,
		)

		klog.InfoS("PSA evaluation results", 
			"namespace", ns.Name, "pod", pod.Name, "level", label,
			"resultCount", len(results))

		for _, result := range results {
			klog.InfoS("PSA evaluation result",
				"namespace", ns.Name, "pod", pod.Name, "level", label,
				"allowed", result.Allowed, "reason", result.ForbiddenReason,
				"detail", result.ForbiddenDetail)
			if !result.Allowed {
				klog.InfoS("User pod violates PSA level",
					"namespace", ns.Name, "pod", pod.Name, "level", label)
				return true, nil // User pod violates the level
			}
		}
	}

	return false, nil // User pods all pass - violation is from service accounts
}