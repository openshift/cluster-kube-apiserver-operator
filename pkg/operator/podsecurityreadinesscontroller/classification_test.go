package podsecurityreadinesscontroller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	psapi "k8s.io/pod-security-admission/api"
	"k8s.io/pod-security-admission/policy"
)

func TestClassifyViolatingNamespaceWithAPIErrors(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "error-test-ns",
		},
	}

	fakeClient := fake.NewSimpleClientset()
	fakeClient.PrependReactor("list", "pods", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated API error: connection refused")
	})

	latestVersion := psapi.LatestVersion()

	psaEvaluator, err := policy.NewEvaluator(policy.DefaultChecks(), &latestVersion)
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
		namespace, "restricted",
	)

	if err == nil {
		t.Errorf("Expected error from API failure, got nil")
	}

	if !strings.Contains(err.Error(), "simulated API error") {
		t.Errorf("Expected API error, got: %v", err)
	}

	// Ensure no classifications were made due to the error
	if len(conditions.violatingUnclassifiedNamespaces) != 0 ||
		len(conditions.violatingUserSCCNamespaces) != 0 ||
		len(conditions.violatingOpenShiftNamespaces) != 0 ||
		len(conditions.violatingRunLevelZeroNamespaces) != 0 ||
		len(conditions.violatingDisabledSyncerNamespaces) != 0 {
		t.Errorf("Expected no classifications due to API error, but got: %+v", conditions)
	}
}

func TestClassifyViolatingNamespace(t *testing.T) {
	for _, tt := range []struct {
		name               string
		namespace          *corev1.Namespace
		pods               []corev1.Pod
		enforceLevel       psapi.Level
		expectedConditions podSecurityOperatorConditions
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
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingRunLevelZeroNamespaces: []string{"kube-system"},
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
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingRunLevelZeroNamespaces: []string{"default"},
			},
			expectError: false,
		},
		{
			name: "run-level zero namespace - kube-public",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kube-public",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingRunLevelZeroNamespaces: []string{"kube-public"},
			},
			expectError: false,
		},
		{
			name: "run-level zero namespace - kube-node-lease",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kube-node-lease",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingRunLevelZeroNamespaces: []string{"kube-node-lease"},
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
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingOpenShiftNamespaces: []string{"openshift-test"},
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
			expectedConditions: podSecurityOperatorConditions{
				violatingDisabledSyncerNamespaces: []string{"test-disabled"},
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
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingUserSCCNamespaces: []string{"customer-ns"},
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
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingUserSCCNamespaces: []string{"user-scc-violation-test"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with a pod that passed SA-based SCC, but not PSA",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newServiceAccountPod("sa-pod", "customer-ns"),
			},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingUnclassifiedNamespaces: []string{"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with mixed pods - unknown violation included",
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
			expectedConditions: podSecurityOperatorConditions{
				violatingUserSCCNamespaces:      []string{"customer-ns"},
				violatingUnclassifiedNamespaces: []string{"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "customer namespace with non violating user pod",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newUserSCCPodRestricted("user-pod", "customer-ns"),
			},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				inconclusiveNamespaces: []string{"customer-ns"},
			},
			expectError: true,
		},
		{
			name: "customer namespace with no pods",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods:         []corev1.Pod{},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				inconclusiveNamespaces: []string{"customer-ns"},
			},
			expectError: true,
		},
		{
			name: "customer namespace with pods without SCC annotation",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-without-annotation",
						Namespace: "customer-ns",
						// No SCC annotation
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "container",
								Image: "image",
							},
						},
					},
				},
			},
			enforceLevel: psapi.LevelRestricted,
			expectedConditions: podSecurityOperatorConditions{
				violatingUnclassifiedNamespaces: []string{"customer-ns"},
			},
			expectError: false,
		},
		{
			name: "namespace tested against privileged level",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "customer-ns",
				},
			},
			pods: []corev1.Pod{
				newUserSCCPodPrivileged("user-pod", "customer-ns"),
			},
			enforceLevel: psapi.LevelPrivileged,
			expectedConditions: podSecurityOperatorConditions{
				inconclusiveNamespaces: []string{"customer-ns"},
			},
			expectError: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controller, err := createTestController(tt.pods)
			if err != nil {
				t.Fatal(err)
			}

			conditions := podSecurityOperatorConditions{}
			err = controller.classifyViolatingNamespace(
				context.Background(), &conditions,
				tt.namespace, tt.enforceLevel,
			)
			if hasError := err != nil; hasError != tt.expectError {
				t.Errorf("classifyViolatingNamespace() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if err != nil {
				return
			}

			if !deepEqualPodSecurityOperatorConditions(&conditions, &tt.expectedConditions) {
				t.Errorf("Conditions mismatch.\nHave: %+v\nWant: %+v", conditions, tt.expectedConditions)
			}
		})
	}
}

func createTestController(pods []corev1.Pod) (*PodSecurityReadinessController, error) {
	fakeClient := fake.NewSimpleClientset()

	for _, pod := range pods {
		_, err := fakeClient.CoreV1().
			Pods(pod.Namespace).
			Create(context.Background(), &pod, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("Failed to create test pod: %v", err)
		}
	}

	latestVersion := psapi.LatestVersion()

	psaEvaluator, err := policy.NewEvaluator(policy.DefaultChecks(), &latestVersion)
	if err != nil {
		return nil, fmt.Errorf("Failed to create PSA evaluator: %v", err)
	}

	return &PodSecurityReadinessController{
		kubeClient:   fakeClient,
		psaEvaluator: psaEvaluator,
	}, nil
}

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

func deepEqualPodSecurityOperatorConditions(
	a, b *podSecurityOperatorConditions,
) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	return slices.Equal(a.violatingOpenShiftNamespaces, b.violatingOpenShiftNamespaces) &&
		slices.Equal(a.violatingRunLevelZeroNamespaces, b.violatingRunLevelZeroNamespaces) &&
		slices.Equal(a.violatingUnclassifiedNamespaces, b.violatingUnclassifiedNamespaces) &&
		slices.Equal(a.violatingDisabledSyncerNamespaces, b.violatingDisabledSyncerNamespaces) &&
		slices.Equal(a.violatingUserSCCNamespaces, b.violatingUserSCCNamespaces) &&
		slices.Equal(a.inconclusiveNamespaces, b.inconclusiveNamespaces)
}
