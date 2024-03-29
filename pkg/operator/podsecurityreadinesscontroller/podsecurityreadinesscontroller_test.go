package podsecurityreadinesscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	psapi "k8s.io/pod-security-admission/api"
)

func TestPodSecurityViolationController(t *testing.T) {
	for _, tt := range []struct {
		name string

		warnings  []string
		namespace *corev1.Namespace

		expectedViolation    bool
		expectedEnforceLabel string
	}{
		{
			name: "violating against restricted namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "restricted",
						psapi.WarnLevelLabel:  "restricted",
					},
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
		},
		{
			name: "violating against baseline namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "baseline",
						psapi.WarnLevelLabel:  "baseline",
					},
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "baseline",
		},
		{
			name:     "non-violating against privileged namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "privileged",
						psapi.WarnLevelLabel:  "privileged",
					},
				},
			},
			expectedEnforceLabel: "privileged",
		},
		{
			name: "violating against unset namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
		},
		{
			name: "violating against mixed alert labels namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "privileged",
						psapi.WarnLevelLabel:  "restricted",
					},
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
		},
		{
			name:     "non-violating against enforced namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.EnforceLevelLabel: "privileged",
					},
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "",
		},
		{
			name: "violating against enforced namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.EnforceLevelLabel: "privileged",
					},
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "",
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset()
			fakeClient.PrependReactor("patch", "namespaces", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
				patchAction, ok := action.(clienttesting.PatchAction)
				if !ok {
					return false, nil, fmt.Errorf("invalid action type")
				}

				patchBytes := patchAction.GetPatch()
				patchMap := make(map[string]interface{})
				if err := json.Unmarshal(patchBytes, &patchMap); err != nil {
					return false, nil, fmt.Errorf("failed to unmarshal patch: %v", err)
				}

				metadata, ok := patchMap["metadata"].(map[string]interface{})
				if !ok {
					return false, nil, fmt.Errorf("patch does not contain metadata")
				}

				labels, ok := metadata["labels"].(map[string]interface{})
				if !ok {
					return false, nil, fmt.Errorf("patch does not contain labels")
				}

				// Check if the expected label is set correctly
				if labels[psapi.EnforceLevelLabel] != tt.expectedEnforceLabel {
					return false, nil, fmt.Errorf("expected enforce label %s, got %s", tt.expectedEnforceLabel, labels[psapi.EnforceLevelLabel])
				}

				return true, nil, nil
			})

			controller := &PodSecurityReadinessController{
				kubeClient: fakeClient,
				warningsHandler: &warningsHandler{
					warnings: tt.warnings,
				},
			}

			isViolating, err := controller.isNamespaceViolating(context.TODO(), tt.namespace)
			if err != nil {
				t.Error(err)
			}

			if isViolating != tt.expectedViolation {
				t.Errorf("expected violation %v, got %v", tt.expectedViolation, isViolating)
			}
		})
	}
}
