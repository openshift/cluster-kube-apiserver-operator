package podsecurityreadinesscontroller

import (
	"context"
	"fmt"
	"testing"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	applyconfiguration "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	psapi "k8s.io/pod-security-admission/api"
)

// Need to add managed fields to mock namespaces, since violations are only checked for labels managed by the syncer
var managedFields = []metav1.ManagedFieldsEntry{
	{
		Manager:   syncerControllerName,
		Operation: "Apply",
		FieldsV1: &metav1.FieldsV1{
			Raw: []byte(
				fmt.Sprintf(`{"f:metadata":{"f:annotations":{"f:%s":{}},"f:labels":{"f:%s":{},"f:%s":{},"f:%s":{}}}}`,
					securityv1.MinimallySufficientPodSecurityStandard,
					psapi.WarnLevelLabel,
					psapi.AuditLevelLabel,
					psapi.EnforceLevelLabel,
				),
			),
		},
	},
}

func TestIsNamespaceViolating(t *testing.T) {
	tests := []struct {
		name            string
		namespace       *corev1.Namespace
		warnings        []string
		setupMockClient func() kubernetes.Interface
		expectViolating bool
		expectError     bool
	}{
		{
			name: "namespace with MinimallySufficientPodSecurityStandard annotation and no violations",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns-1",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "restricted",
					},
				},
			},
			warnings: []string{},
			setupMockClient: func() kubernetes.Interface {
				return &mockKubeClientWithResponse{}
			},
			expectViolating: false,
			expectError:     false,
		},
		{
			name: "namespace with MinimallySufficientPodSecurityStandard annotation and violations",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns-2",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "restricted",
					},
				},
			},
			warnings: []string{"violation found"},
			setupMockClient: func() kubernetes.Interface {
				return &mockKubeClientWithResponse{}
			},
			expectViolating: true,
			expectError:     false,
		},
		{
			name: "namespace with no annotation",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-ns-5",
					Labels: map[string]string{},
				},
			},
			warnings: []string{},
			setupMockClient: func() kubernetes.Interface {
				return &mockKubeClientWithResponse{}
			},
			expectViolating: false,
			expectError:     true,
		},
		{
			name: "Apply returns error",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns-6",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "restricted",
					},
				},
			},
			warnings: []string{},
			setupMockClient: func() kubernetes.Interface {
				return &mockKubeClientWithResponse{
					error: fmt.Errorf("apply error"),
				}
			},
			expectViolating: false,
			expectError:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockWarnings := &warningsHandler{
				warnings: tc.warnings,
			}

			controller := &PodSecurityReadinessController{
				kubeClient:      tc.setupMockClient(),
				warningsHandler: mockWarnings,
			}

			tc.namespace.ManagedFields = managedFields

			violating, err := controller.isNamespaceViolating(context.Background(), tc.namespace)

			if (err != nil) != tc.expectError {
				t.Errorf("isNamespaceViolating() error = %v, expectError %v", err, tc.expectError)
				return
			}

			if violating != tc.expectViolating {
				t.Errorf("isNamespaceViolating() violating = %v, expectViolating %v", violating, tc.expectViolating)
			}
		})
	}
}

type mockKubeClientWithResponse struct {
	kubernetes.Interface
	error error
}

func (m *mockKubeClientWithResponse) CoreV1() typedcorev1.CoreV1Interface {
	return &mockCoreV1WithResponse{error: m.error}
}

type mockCoreV1WithResponse struct {
	typedcorev1.CoreV1Interface
	error error
}

func (m *mockCoreV1WithResponse) Namespaces() typedcorev1.NamespaceInterface {
	return &mockNamespaceInterfaceWithResponse{error: m.error}
}

type mockNamespaceInterfaceWithResponse struct {
	typedcorev1.NamespaceInterface
	error error
}

func (m *mockNamespaceInterfaceWithResponse) Apply(ctx context.Context, nsApply *applyconfiguration.NamespaceApplyConfiguration, opts metav1.ApplyOptions) (*corev1.Namespace, error) {
	return nil, m.error
}
