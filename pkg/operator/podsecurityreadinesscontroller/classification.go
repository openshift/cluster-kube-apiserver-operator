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

	// Evaluate by individual pod.
	allPods, err := c.kubeClient.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to list pods in namespace", "namespace", ns.Name)
		return err
	}

	isViolating := createPodViolationEvaluator(c.psaEvaluator, enforceLevel)
	violatingPods := []corev1.Pod{}
	for _, pod := range allPods.Items {
		if isViolating(pod) {
			violatingPods = append(violatingPods, pod)
		}
	}
	if len(violatingPods) == 0 {
		conditions.addInconclusive(ns)
		klog.V(2).InfoS("no violating pods found in namespace, marking as inconclusive", "namespace", ns.Name)
		return nil
	}

	violatingUserSCCPods := []corev1.Pod{}
	for _, pod := range violatingPods {
		if pod.Annotations[securityv1.ValidatedSCCSubjectTypeAnnotation] == "user" {
			violatingUserSCCPods = append(violatingUserSCCPods, pod)
		}
	}
	if len(violatingUserSCCPods) > 0 {
		conditions.addViolatingUserSCC(ns)
	}
	if len(violatingUserSCCPods) != len(violatingPods) {
		conditions.addUnclassifiedIssue(ns)
	}

	return nil
}

func createPodViolationEvaluator(evaluator policy.Evaluator, enforcement psapi.Level) func(pod corev1.Pod) bool {
	return func(pod corev1.Pod) bool {
		results := evaluator.EvaluatePod(
			psapi.LevelVersion{
				Level:   enforcement,
				Version: psapi.LatestVersion(),
			},
			&pod.ObjectMeta,
			&pod.Spec,
		)

		for _, result := range results {
			if !result.Allowed {
				return true
			}
		}
		return false
	}
}
