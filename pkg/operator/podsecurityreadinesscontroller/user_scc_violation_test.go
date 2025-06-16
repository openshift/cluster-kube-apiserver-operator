package podsecurityreadinesscontroller

import (
	"context"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/pod-security-admission/policy"
)

func TestUserSCCViolationConditionDetection(t *testing.T) {
	tests := []struct {
		name                     string
		namespace                *corev1.Namespace
		pods                     []*corev1.Pod
		expectedUserViolation    bool
		expectedConditionStatus  operatorv1.ConditionStatus
		expectedConditionMessage string
	}{
		{
			name: "user pod violates baseline PSA with privileged container",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-user-violation",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "baseline",
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "user-violating-pod",
						Namespace: "test-user-violation",
						Annotations: map[string]string{
							securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
							"openshift.io/scc":                           "privileged",
						},
					},
					Spec: corev1.PodSpec{
						HostNetwork: true, // Violates baseline
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "test:latest",
								SecurityContext: &corev1.SecurityContext{
									Privileged: &[]bool{true}[0], // Violates baseline
								},
							},
						},
					},
				},
			},
			expectedUserViolation:    true,
			expectedConditionStatus:  operatorv1.ConditionTrue,
			expectedConditionMessage: "Violations detected in namespaces: [test-user-violation]",
		},
		{
			name: "user pod passes baseline PSA",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-user-compliant",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "baseline",
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "user-compliant-pod",
						Namespace: "test-user-compliant",
						Annotations: map[string]string{
							securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
							"openshift.io/scc":                           "restricted",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "test:latest",
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &[]bool{false}[0],
									RunAsNonRoot:             &[]bool{true}[0],
									Capabilities: &corev1.Capabilities{
										Drop: []corev1.Capability{"ALL"},
									},
								},
							},
						},
						SecurityContext: &corev1.PodSecurityContext{
							RunAsNonRoot: &[]bool{true}[0],
						},
					},
				},
			},
			expectedUserViolation:   false,
			expectedConditionStatus: operatorv1.ConditionFalse,
		},
		{
			name: "no user pods - only service account pods",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-user-pods",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "baseline",
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-violating-pod",
						Namespace: "test-no-user-pods",
						Annotations: map[string]string{
							securityv1.ValidatedSCCSubjectTypeAnnotation: "serviceaccount",
							"openshift.io/scc":                           "privileged",
						},
					},
					Spec: corev1.PodSpec{
						HostNetwork: true, // Violates baseline but shouldn't count as user violation
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "test:latest",
							},
						},
					},
				},
			},
			expectedUserViolation:   false,
			expectedConditionStatus: operatorv1.ConditionFalse,
		},
		{
			name: "mixed user and service account pods - user pod violates",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mixed-pods",
					Annotations: map[string]string{
						securityv1.MinimallySufficientPodSecurityStandard: "restricted",
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-violating-pod",
						Namespace: "test-mixed-pods",
						Annotations: map[string]string{
							securityv1.ValidatedSCCSubjectTypeAnnotation: "serviceaccount",
							"openshift.io/scc":                           "privileged",
						},
					},
					Spec: corev1.PodSpec{
						HostNetwork: true,
						Containers: []corev1.Container{
							{
								Name:  "sa-container",
								Image: "test:latest",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "user-violating-pod",
						Namespace: "test-mixed-pods",
						Annotations: map[string]string{
							securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
							"openshift.io/scc":                           "anyuid",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "user-container",
								Image: "test:latest",
								SecurityContext: &corev1.SecurityContext{
									// Missing required restricted settings
									RunAsNonRoot:             &[]bool{false}[0], // Violates restricted
									AllowPrivilegeEscalation: &[]bool{true}[0],  // Violates restricted
								},
							},
						},
					},
				},
			},
			expectedUserViolation:    true,
			expectedConditionStatus:  operatorv1.ConditionTrue,
			expectedConditionMessage: "Violations detected in namespaces: [test-mixed-pods]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with test data
			objects := []runtime.Object{tt.namespace}
			for _, pod := range tt.pods {
				objects = append(objects, pod)
			}
			fakeClient := fake.NewSimpleClientset(objects...)

			// Create PSA evaluator
			psaEvaluator, err := policy.NewEvaluator(policy.DefaultChecks())
			if err != nil {
				t.Fatalf("Failed to create PSA evaluator: %v", err)
			}

			// Create controller
			controller := &PodSecurityReadinessController{
				kubeClient:   fakeClient,
				psaEvaluator: psaEvaluator,
			}

			// Determine enforcement level from namespace annotation
			enforcementLevel := "baseline"
			if level, ok := tt.namespace.Annotations[securityv1.MinimallySufficientPodSecurityStandard]; ok {
				enforcementLevel = level
			}

			// Test isUserViolation method directly
			isUserViolation, err := controller.isUserViolation(context.Background(), tt.namespace, enforcementLevel)
			if err != nil {
				t.Errorf("isUserViolation returned error: %v", err)
			}
			if isUserViolation != tt.expectedUserViolation {
				t.Errorf("Expected isUserViolation=%v, got %v", tt.expectedUserViolation, isUserViolation)
			}

			// Test full classification workflow
			conditions := podSecurityOperatorConditions{}
			err = controller.classifyViolatingNamespace(context.Background(), &conditions, tt.namespace, enforcementLevel)
			if err != nil {
				t.Errorf("classifyViolatingNamespace returned error: %v", err)
			}

			// Verify user SCC violation condition
			conditionFuncs := conditions.toConditionFuncs()
			userSCCConditionFound := false

			for _, conditionFunc := range conditionFuncs {
				// Create a mock status to apply the condition to
				mockStatus := &operatorv1.OperatorStatus{}
				err := conditionFunc(mockStatus)
				if err != nil {
					t.Errorf("Condition function returned error: %v", err)
					continue
				}

				// Check if this is the user SCC condition
				for _, condition := range mockStatus.Conditions {
					if condition.Type == PodSecurityUserSCCType {
						userSCCConditionFound = true
						if condition.Status != tt.expectedConditionStatus {
							t.Errorf("Expected condition status %v, got %v", tt.expectedConditionStatus, condition.Status)
						}
						if tt.expectedConditionMessage != "" && condition.Message != tt.expectedConditionMessage {
							t.Errorf("Expected condition message %q, got %q", tt.expectedConditionMessage, condition.Message)
						}
						t.Logf("User SCC condition: Status=%s, Message=%s", condition.Status, condition.Message)
					}
				}
			}

			if !userSCCConditionFound {
				t.Errorf("User SCC violation condition not found in condition functions")
			}

			// Verify namespace classification
			if tt.expectedUserViolation {
				if len(conditions.userSCCViolationNamespaces) != 1 || conditions.userSCCViolationNamespaces[0] != tt.namespace.Name {
					t.Errorf("Expected namespace %s in userSCCViolationNamespaces, got %v", tt.namespace.Name, conditions.userSCCViolationNamespaces)
				}
			} else {
				if len(conditions.userSCCViolationNamespaces) != 0 {
					t.Errorf("Expected no user SCC violations, got %v", conditions.userSCCViolationNamespaces)
				}
			}
		})
	}
}

func TestUserSCCViolationDetectionWithSpecificPSALevels(t *testing.T) {
	// Test specifically against different PSA levels
	tests := []struct {
		name             string
		enforcementLevel string
		podSpec          corev1.PodSpec
		expectViolation  bool
	}{
		{
			name:             "privileged hostNetwork violates baseline",
			enforcementLevel: "baseline",
			podSpec: corev1.PodSpec{
				HostNetwork: true,
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:latest",
					},
				},
			},
			expectViolation: true,
		},
		{
			name:             "privileged container violates baseline",
			enforcementLevel: "baseline",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:latest",
						SecurityContext: &corev1.SecurityContext{
							Privileged: &[]bool{true}[0],
						},
					},
				},
			},
			expectViolation: true,
		},
		{
			name:             "allowPrivilegeEscalation=true violates restricted",
			enforcementLevel: "restricted",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:latest",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &[]bool{true}[0],
						},
					},
				},
			},
			expectViolation: true,
		},
		{
			name:             "runAsNonRoot=false violates restricted",
			enforcementLevel: "restricted",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:latest",
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot: &[]bool{false}[0],
						},
					},
				},
			},
			expectViolation: true,
		},
		{
			name:             "compliant restricted pod",
			enforcementLevel: "restricted",
			podSpec: corev1.PodSpec{
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: &[]bool{true}[0],
					SeccompProfile: &corev1.SeccompProfile{
						Type: corev1.SeccompProfileTypeRuntimeDefault,
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:latest",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &[]bool{false}[0],
							RunAsNonRoot:             &[]bool{true}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
			expectViolation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-psa-levels",
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-user-pod",
					Namespace: namespace.Name,
					Annotations: map[string]string{
						securityv1.ValidatedSCCSubjectTypeAnnotation: "user",
					},
				},
				Spec: tt.podSpec,
			}

			fakeClient := fake.NewSimpleClientset(namespace, pod)
			psaEvaluator, err := policy.NewEvaluator(policy.DefaultChecks())
			if err != nil {
				t.Fatalf("Failed to create PSA evaluator: %v", err)
			}

			controller := &PodSecurityReadinessController{
				kubeClient:   fakeClient,
				psaEvaluator: psaEvaluator,
			}

			isUserViolation, err := controller.isUserViolation(context.Background(), namespace, tt.enforcementLevel)
			if err != nil {
				t.Errorf("isUserViolation returned error: %v", err)
			}

			if isUserViolation != tt.expectViolation {
				t.Errorf("Expected violation=%v for level %s, got %v", tt.expectViolation, tt.enforcementLevel, isUserViolation)
			}

			t.Logf("PSA level %s: violation=%v (expected %v)", tt.enforcementLevel, isUserViolation, tt.expectViolation)
		})
	}
}

