package podsecurityreadinesscontroller

import (
	"context"
	"strings"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"
	"k8s.io/pod-security-admission/policy"
)

var (
	runLevelZeroNamespaces = sets.New[string](
		"default",
		"kube-system",
		"kube-public",
		"kube-node-lease",
	)
)

func (c *PodSecurityReadinessController) classifyViolatingNamespace(
	ctx context.Context,
	conditions *podSecurityOperatorConditions,
	ns *corev1.Namespace,
	enforceLevel psapi.Level,
) error {
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

	allPods, err := c.kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to list pods in namespace", "namespace", ns.Name)
		return err
	}
	if hasUserSCCViolatingPod(c.psaEvaluator, enforceLevel, allPods.Items) {
		conditions.addViolatingUserSCC(ns)
		return nil
	}

	conditions.addUnclassifiedIssue(ns)

	return nil
}

func hasUserSCCViolatingPod(
	psaEvaluator policy.Evaluator,
	enforcementLevel psapi.Level,
	pods []corev1.Pod,
) bool {
	var userPods []corev1.Pod
	for _, pod := range pods {
		if strings.HasPrefix(pod.Annotations[securityv1.ValidatedSCCAnnotation], "restricted-v") {
			// If the SCC evaluation is restricted-v*, it shouldn't be possible
			// to violate as a user-based SCC.
			continue
		}

		if pod.Annotations[securityv1.ValidatedSCCSubjectTypeAnnotation] == "user" {
			userPods = append(userPods, pod)
		}
	}
	if len(userPods) == 0 {
		return false // No user pods = violation is based upon service accounts
	}

	enforcement := psapi.LevelVersion{
		Level:   enforcementLevel,
		Version: psapi.LatestVersion(),
	}
	for _, pod := range userPods {
		results := psaEvaluator.EvaluatePod(
			enforcement,
			&pod.ObjectMeta,
			&pod.Spec,
		)

		// results contains between 1 and 2 elements
		for _, result := range results {
			if !result.Allowed {
				return true
			}
		}
	}

	return false
}
