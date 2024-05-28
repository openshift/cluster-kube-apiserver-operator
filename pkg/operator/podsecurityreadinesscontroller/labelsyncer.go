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
	policyControllerName  = "cluster-policy-controller"
	labelSyncControlLabel = "security.openshift.io/scc.podSecurityLabelSync"
)

var (
	alertLabels = []string{
		psapi.WarnLevelLabel,
		psapi.AuditLevelLabel,
	}
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
	ownedLabels := sets.New[string]()
	for _, fieldEntry := range ns.ManagedFields {
		if !isSyncController(fieldEntry.Manager) {
			continue
		}

		managedLabels, err := managedLabels(fieldEntry, isAlertLabel)
		if err != nil {
			klog.Errorf("failed to extract managed fields for NS %q: %v", ns.Name, err)
			// In case of doubt, assume we manage the namespace. Clusters that
			// are having `isNSControlled(ns) == false` only violations will be
			// ignored.
			return true
		}

		if managedLabels.Len() > 0 {
			ownedLabels = ownedLabels.Union(managedLabels)
		}
	}

	// Verify that alert labels, that are used to check for violation are owned by us.
	return ownedLabels.HasAll(alertLabels...)
}

func isAlertLabel(label string) bool {
	for _, l := range alertLabels {
		if label == l {
			return true
		}
	}

	return false
}

func isSyncController(name string) bool {
	return len(name) == 0 ||
		name == policyControllerName ||
		name == syncerControllerName
}

// managedLabels extract the metadata.labels from the JSON in the managedEntry.FieldsV1
// that describes the object's field ownership
func managedLabels(fieldsEntry metav1.ManagedFieldsEntry, filter func(string) bool) (sets.Set[string], error) {
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
		label := strings.Replace(l, "f:", "", 1)
		if filter(label) {
			ret.Insert(label)
		}
	}

	return ret, nil
}
