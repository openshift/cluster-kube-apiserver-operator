package podsecurityreadinesscontroller

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"
)

const (
	syncerControllerName  = "pod-security-admission-label-synchronization-controller"
	labelSyncControlLabel = "security.openshift.io/scc.podSecurityLabelSync"
)

func isNSControlled(ns *corev1.Namespace) bool {
	// The customer explicitly tells us to manage the namespace.
	if ns.Labels[labelSyncControlLabel] == "true" {
		return true
	}

	// The customer explicitly tells us to not manage the namespace.
	if ns.Labels[labelSyncControlLabel] == "false" {
		return false
	}

	// Check who is managing the labels.
	extractedPerManager, err := newLabelsToManager(ns)
	if err != nil {
		klog.Errorf("ns extraction failed: %v", err)
		return false
	}

	for _, labelName := range []string{
		psapi.EnforceLevelLabel, psapi.WarnLevelLabel, psapi.AuditLevelLabel,
	} {
		if _, ok := ns.Labels[labelName]; ok {
			// If the label is set, we need to verify that it is managed by us.
			manager := extractedPerManager[labelName]
			if len(manager) > 0 && manager != "cluster-policy-controller" && manager != syncerControllerName {
				// The customer is managing at least one of the labels.
				return false
			}
		}
	}

	// We manage all labels.
	return true
}

type labelsToManager map[string]string

func newLabelsToManager(ns *corev1.Namespace) (labelsToManager, error) {
	m := labelsToManager{}

	for _, fieldEntry := range ns.ManagedFields {
		managedLabels, err := managedLabels(fieldEntry)
		if err != nil {
			return nil, fmt.Errorf("failed to extract managed fields for NS %q: %v", ns.Name, err)
		}

		for label := range managedLabels {
			m[label] = fieldEntry.Manager
		}
	}

	return m, nil
}

// managedLabels extract the metadata.labels from the JSON in the managedEntry.FieldsV1
// that describes the object's field ownership
func managedLabels(fieldsEntry metav1.ManagedFieldsEntry) (sets.Set[string], error) {
	managedUnstructured := map[string]interface{}{}
	err := json.Unmarshal(fieldsEntry.FieldsV1.Raw, &managedUnstructured)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal managed fields: %w", err)
	}

	labels, found, err := unstructured.NestedMap(managedUnstructured, "f:metadata", "f:labels")
	if err != nil {
		return nil, fmt.Errorf("failed to get labels from the managed fields: %w", err)
	}

	ret := sets.New[string]()
	if !found {
		return ret, nil
	}

	for l := range labels {
		ret.Insert(strings.Replace(l, "f:", "", 1))
	}

	return ret, nil
}
