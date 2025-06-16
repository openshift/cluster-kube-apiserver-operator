package podsecurityreadinesscontroller

import (
	"context"
	"testing"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/pod-security-admission/policy"
)

func TestClassifyViolatingNamespace(t *testing.T) {
	for _, tt := range []struct {
		name               string
		namespace          *corev1.Namespace
		pods               []corev1.Pod
		enforceLevel       string
		expectedConditions map[string][]string
		expectError        bool
	}{
		{
			name: "run-level zero namespace - kube-system",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kube-system",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"runLevelZero": {"kube-system"},
			},
			expectError: false,
		},
		{
			name: "run-level zero namespace - default",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"runLevelZero": {"default"},
			},
			expectError: false,
		},
		{
			name: "openshift namespace",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-test",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"openshift": {"openshift-test"},
			},
			expectError: false,
		},
		{
			name: "disabled syncer namespace",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-disabled",
					Labels: map[string]string{
						"security.openshift.io/scc.podSecurityLabelSync": "false",
					},
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"disabledSyncer": {"test-disabled"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with user SCC violation",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newUserSCCPodPrivileged("user-pod", "customer-ns"),
			},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"userSCC": {"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "user pod with privileged container - exact test manifest case",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "user-scc-violation-test",
				},
			},
			pods: []corev1.Pod{
				newUserSCCPodWithPrivilegedContainer("user-scc-violating-pod", "user-scc-violation-test"),
			},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"userSCC": {"user-scc-violation-test"},
			},
			expectError: false,
		},
		{
			name: "customer namespace without user SCC violation",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newServiceAccountPod("sa-pod", "customer-ns"),
			},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"customer": {"customer-ns"},
			},
			expectError: false,
		},
		{
			// TODO: Ideally we would not drop the "customer" condition.
			name: "customer namespace with mixed pods - user violates",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newServiceAccountPod("sa-pod", "customer-ns"),
				newUserSCCPodPrivileged("user-pod", "customer-ns"),
			},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"userSCC": {"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with user pods that pass PSA",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newUserSCCPodRestricted("user-pod", "customer-ns"),
			},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"customer": {"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with no pods",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: "restricted",
			expectedConditions: map[string][]string{
				"customer": {"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "invalid PSA level causes error",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods:               []corev1.Pod{},
			enforceLevel:       "invalid-level",
			expectedConditions: map[string][]string{},
			expectError:        true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset()

			// Add pods to fake client
			for _, pod := range tt.pods {
				_, err := fakeClient.CoreV1().Pods(tt.namespace.Name).Create(context.Background(), &pod, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create test pod: %v", err)
				}
			}

			psaEvaluator, err := policy.NewEvaluator(policy.DefaultChecks())
			if err != nil {
				t.Fatalf("Failed to create PSA evaluator: %v", err)
			}

			controller := &PodSecurityReadinessController{
				kubeClient:   fakeClient,
				psaEvaluator: psaEvaluator,
			}

			conditions := podSecurityOperatorConditions{}

			err = controller.classifyViolatingNamespace(
				context.Background(), &conditions,
				tt.namespace, tt.enforceLevel,
			)

			if (err != nil) != tt.expectError {
				t.Errorf("classifyViolatingNamespace() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if err != nil {
				return // Expected error, nothing more to check
			}

			// Verify the conditions were set correctly
			for conditionType, expectedNamespaces := range tt.expectedConditions {
				var actualNamespaces []string
				switch conditionType {
				case "runLevelZero":
					actualNamespaces = conditions.violatingRunLevelZeroNamespaces
				case "openshift":
					actualNamespaces = conditions.violatingOpenShiftNamespaces
				case "disabledSyncer":
					actualNamespaces = conditions.violatingDisabledSyncerNamespaces
				case "customer":
					actualNamespaces = conditions.violatingCustomerNamespaces
				case "userSCC":
					actualNamespaces = conditions.userSCCViolationNamespaces
				}

				if len(actualNamespaces) != len(expectedNamespaces) {
					t.Errorf("expected %d %s namespaces, got %d", len(expectedNamespaces), conditionType, len(actualNamespaces))
				}

				for _, expected := range expectedNamespaces {
					found := false
					for _, actual := range actualNamespaces {
						if actual == expected {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected %s namespace %s not found in %v", conditionType, expected, actualNamespaces)
					}
				}
			}
		})
	}
}

// Test pod creation helpers
func newUserSCCPodPrivileged(name, namespace string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
			},
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &[]bool{false}[0],
			},
			Containers: []corev1.Container{
				{
					Name:  "container",
					Image: "image",
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: &[]bool{true}[0],
					},
				},
			},
		},
	}
}

func newServiceAccountPod(name, namespace string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				securityv1.ValidatedSCCSubjectTypeAnnotation: "service-account",
			},
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &[]bool{false}[0],
			},
		},
	}
}

func newUserSCCPodWithPrivilegedContainer(name, namespace string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "test-container",
					Image:   "busybox:latest",
					Command: []string{"sleep", "infinity"},
					SecurityContext: &corev1.SecurityContext{
						Privileged:   &[]bool{true}[0],
						RunAsNonRoot: &[]bool{false}[0],
						RunAsUser:    &[]int64{0}[0],
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
}

func newUserSCCPodRestricted(name, namespace string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
			},
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &[]bool{true}[0],
				RunAsUser:      &[]int64{1000}[0],
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				FSGroup:        &[]int64{1000}[0],
			},
			Containers: []corev1.Container{
				{
					Name:  "container",
					Image: "image",
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: &[]bool{false}[0],
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						RunAsNonRoot: &[]bool{true}[0],
					},
				},
			},
		},
	}
}
