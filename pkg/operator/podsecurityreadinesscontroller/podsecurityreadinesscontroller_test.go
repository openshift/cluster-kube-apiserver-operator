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
	userFields := []metav1.ManagedFieldsEntry{{
		Manager: "kubectl-edit",
		FieldsV1: &metav1.FieldsV1{
			Raw: []byte(`{
				"f:metadata": {
					"f:labels": {
						"f:pod-security.kubernetes.io/audit": {},
						"f:pod-security.kubernetes.io/audit-version": {},
						"f:pod-security.kubernetes.io/warn": {},
						"f:pod-security.kubernetes.io/warn-version": {}
					}
				}
			}`),
		},
	}}

	mixedFields := []metav1.ManagedFieldsEntry{
		{
			Manager: "kubectl-edit",
			FieldsV1: &metav1.FieldsV1{
				Raw: []byte(`{
					"f:metadata": {
						"f:labels": {
							"f:pod-security.kubernetes.io/warn": {},
							"f:pod-security.kubernetes.io/warn-version": {}
						}
					}
				}`),
			},
			Operation: metav1.ManagedFieldsOperationApply,
		},
		{
			Manager: "pod-security-admission-label-synchronization-controller",
			FieldsV1: &metav1.FieldsV1{
				Raw: []byte(`{
					"f:metadata": {
						"f:labels": {
							"f:pod-security.kubernetes.io/audit": {},
							"f:pod-security.kubernetes.io/audit-version": {}
						}
					}
				}`),
			},
			Operation: metav1.ManagedFieldsOperationApply,
		},
	}

	syncFields := []metav1.ManagedFieldsEntry{{
		Manager: "pod-security-admission-label-synchronization-controller",
		FieldsV1: &metav1.FieldsV1{
			Raw: []byte(`{
				"f:metadata": {
					"f:annotations": {
				    "f:security.openshift.io/MinimallySufficientPodSecurityStandard": {}
					},
					"f:labels": {
						"f:pod-security.kubernetes.io/audit": {},
						"f:pod-security.kubernetes.io/audit-version": {}
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
			name: "violating against restricted namespace by sync annotation",
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
			name: "violating against restricted namespace by psa label",
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
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
			expectedError:        false,
		},
		{
			name: "violating against restricted namespace by sync annotation (taking priority over psa label)",
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
					Labels: map[string]string{
						psapi.AuditLevelLabel: "baseline",
						psapi.WarnLevelLabel:  "baseline",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
			expectedError:        false,
		},
		{
			name:     "non-violating against restricted namespace by sync annotation (taking priority over psa label)",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "baseline",
					},
					Labels: map[string]string{
						psapi.AuditLevelLabel: "restricted",
						psapi.WarnLevelLabel:  "restricted",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "baseline",
			expectedError:        false,
		},
		{
			name: "violating against baseline namespace by sync annotation",
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
			name: "violating against baseline namespace by psa label",
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
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "baseline",
			expectedError:        false,
		},
		{
			name:     "non-violating against privileged namespace by sync annotation",
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
			name:     "non-violating against privileged namespace by psa label",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "privileged",
						psapi.WarnLevelLabel:  "privileged",
					},
					ManagedFields: syncFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "privileged",
			expectedError:        false,
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
					ManagedFields: mixedFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
			expectedError:        false,
		},
		{
			name: "violating against mixed ownership namespace",
			warnings: []string{
				"existing pods in namespace \"violating-namespace\" violate the new PodSecurity enforce level \"restricted:latest\"",
				"violating-pod: allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile",
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "restricted",
						psapi.WarnLevelLabel:  "privileged",
					},
					ManagedFields: mixedFields,
				},
			},
			expectedViolation:    true,
			expectedEnforceLabel: "restricted",
		},
		{
			name:     "non violating against mixed ownership namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "privileged",
						psapi.WarnLevelLabel:  "restricted",
					},
					ManagedFields: mixedFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "privileged",
			expectedError:        false,
		},
		{
			name:     "non violating against no-ownership namespace",
			warnings: []string{},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "violating-namespace",
					Labels: map[string]string{
						psapi.AuditLevelLabel: "privileged",
						psapi.WarnLevelLabel:  "restricted",
					},
					ManagedFields: userFields,
				},
			},
			expectedViolation:    false,
			expectedEnforceLabel: "",
			expectedError:        true,
		},
		{
			name:     "error against inconclusive namespace",
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
