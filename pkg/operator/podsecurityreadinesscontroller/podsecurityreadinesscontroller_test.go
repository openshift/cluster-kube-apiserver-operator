package podsecurityreadinesscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	psapi "k8s.io/pod-security-admission/api"
)

func TestPodSecurityViolationController(t *testing.T) {
	syncFields := []metav1.ManagedFieldsEntry{{
		Manager: "pod-security-admission-label-synchronization-controller",
		FieldsV1: &metav1.FieldsV1{
			Raw: []byte(`{
				"f:metadata": {
					"f:annotations": {
					    "f:security.openshift.io/MinimallySufficientPodSecurityStandard": {}
					}
				}
			}`),
		},
		Operation: metav1.ManagedFieldsOperationApply,
	}}

	for _, tt := range []struct {
		name string

		warnings  []string
		namespace *corev1.Namespace

		expectedViolation    bool
		expectedEnforceLabel string
		expectedError        bool
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
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "restricted",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
			expectedError:        false,
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
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "baseline",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "baseline",
			expectedError:        false,
		},
		{
			name:     "non-violating against privileged namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "privileged",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "privileged",
			expectedError:        false,
		},
		{
			name:     "non violating against inconclusive namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:          "violating-namespace",
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "",
			expectedError:        true,
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
			if (err != nil) != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}

			if isViolating != tt.expectedViolation {
				t.Errorf("expected violation %v, got %v", tt.expectedViolation, isViolating)
			}
		})
	}
}

func TestNamespaceSelector(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ns-without-enforce",
				Labels: map[string]string{},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns-with-enforce",
				Labels: map[string]string{
					psapi.EnforceLevelLabel: "restricted",
				},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "another-ns-without-enforce",
				Labels: map[string]string{},
			},
		},
	)

	selector, err := nonEnforcingSelector()
	if err != nil {
		t.Fatal(err)
	}

	nsList, err := fakeClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		t.Fatal(err)
	}

	if len(nsList.Items) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(nsList.Items))
	}

	for _, ns := range nsList.Items {
		label, ok := ns.Labels[psapi.EnforceLevelLabel]
		if ok {
			t.Error("unexpected enforce label", label)
		}
	}
}
