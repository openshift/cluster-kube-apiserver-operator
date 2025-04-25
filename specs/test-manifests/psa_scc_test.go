package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	pollTimeout     = 30 * time.Second
	pollInterval    = 5 * time.Second
	immediate       = true
	namespacePrefix = "test-baseline-user-scc"
)

// warningsHandler collects the warnings and makes them available.
type warningsHandler struct {
	warnings []string
}

// HandleWarningHeader implements the WarningHandler interface. It stores the
// warning headers.
func (w *warningsHandler) HandleWarningHeader(code int, agent string, text string) {
	if text == "" {
		return
	}

	w.warnings = append(w.warnings, text)
}

// PopAll returns all warnings and clears the slice.
func (w *warningsHandler) PopAll() []string {
	warnings := w.warnings
	w.warnings = []string{}

	return warnings
}

func TestPSAUserSCCViolationDetection(t *testing.T) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG environment variable not set, skipping integration test")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("Failed to build config from kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create kubernetes clientset: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create dynamic client: %v", err)
	}

	ctx := context.Background()
	var namespace string

	// Cleanup function
	// defer func() {

	// 	time.Sleep(10 * time.Minute)

	// 	if namespace == "" {
	// 		return
	// 	}

	// 	t.Logf("Cleaning up test namespace: %s", namespace)

	// 	err := clientset.CoreV1().Namespaces().Delete(
	// 		ctx,
	// 		namespace,
	// 		metav1.DeleteOptions{},
	// 	)
	// 	if err != nil && !apierrors.IsNotFound(err) {
	// 		t.Logf("Warning: failed to delete test namespace: %v", err)
	// 	}

	// 	// Wait for namespace deletion
	// 	err = wait.PollUntilContextTimeout(
	// 		ctx,
	// 		pollInterval,
	// 		pollTimeout,
	// 		true,
	// 		func(ctx context.Context) (bool, error) {
	// 			_, err := clientset.CoreV1().Namespaces().Get(
	// 				ctx,
	// 				namespace,
	// 				metav1.GetOptions{},
	// 			)
	// 			return apierrors.IsNotFound(err), nil
	// 		},
	// 	)
	// 	if err != nil {
	// 		t.Logf("Warning: timeout waiting for namespace deletion: %v", err)
	// 	}
	// }()

	t.Run("CreateTestNamespace", func(t *testing.T) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: namespacePrefix + "-",
				Labels: map[string]string{
					// If kube-apiserver is in enforce mode, there is no other way.
					"pod-security.kubernetes.io/enforce":         "privileged",
					"pod-security.kubernetes.io/audit":           "privileged",
					"pod-security.kubernetes.io/warn":            "privileged",
					"pod-security.kubernetes.io/audit-version":   "latest",
					"pod-security.kubernetes.io/warn-version":    "latest",
					"pod-security.kubernetes.io/enforce-version": "latest",
				},
			},
		}

		createdNS, err := clientset.CoreV1().Namespaces().Create(
			ctx,
			ns,
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to create test namespace: %v", err)
		}

		namespace = createdNS.Name
		t.Logf("Successfully created test namespace: %s", namespace)
	})

	// t.Run("VerifyNamespaceLabels", func(t *testing.T) {
	// 	expectedLabels := map[string]string{
	// 		"pod-security.kubernetes.io/audit": "restricted",
	// 		"pod-security.kubernetes.io/warn":  "restricted",
	// 	}

	// 	err := wait.PollUntilContextTimeout(
	// 		ctx,
	// 		pollInterval,
	// 		pollTimeout,
	// 		immediate,
	// 		func(ctx context.Context) (bool, error) {
	// 			ns, err := clientset.CoreV1().Namespaces().Get(
	// 				ctx,
	// 				namespace,
	// 				metav1.GetOptions{},
	// 			)
	// 			if err != nil {
	// 				return false, err
	// 			}

	// 			for key, expectedValue := range expectedLabels {
	// 				actualValue, exists := ns.Labels[key]
	// 				if !exists {
	// 					t.Logf("Waiting for label %s to appear", key)
	// 					return false, nil
	// 				}
	// 				if actualValue != expectedValue {
	// 					return false, fmt.Errorf(
	// 						"label %s has value %s, expected %s",
	// 						key, actualValue, expectedValue,
	// 					)
	// 				}
	// 			}

	// 			return true, nil
	// 		},
	// 	)

	// 	if err != nil {
	// 		t.Fatalf("Failed to verify namespace labels: %v", err)
	// 	}

	// 	t.Logf("Successfully verified namespace labels")
	// })

	t.Run("CreateViolatingPod", func(t *testing.T) {
		// Check for SCC-related annotations
		expectedAnnotations := []string{
			"openshift.io/scc",
			"security.openshift.io/validated-scc-subject-type",
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-baseline-violating-pod",
				Namespace: namespace,
			},
			Spec: corev1.PodSpec{
				HostNetwork: true, // Violates baseline PSA policy
				Containers: []corev1.Container{
					{
						Name:    "test-container",
						Image:   "registry.redhat.io/ubi8/ubi-minimal:latest",
						Command: []string{"sleep", "3600"},
					},
				},
				RestartPolicy: corev1.RestartPolicyNever,
			},
		}

		_, err := clientset.CoreV1().Pods(namespace).Create(
			ctx,
			pod,
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to create violating pod: %v", err)
		}

		// Wait for pod to have SCC annotations with expected values
		err = wait.PollUntilContextTimeout(
			ctx,
			pollInterval,
			pollTimeout,
			immediate,
			func(ctx context.Context) (bool, error) {
				pod, err := clientset.CoreV1().Pods(namespace).Get(
					ctx,
					"user-baseline-violating-pod",
					metav1.GetOptions{},
				)
				if err != nil {
					return false, err
				}

				// Check that all expected annotations exist
				for _, annotation := range expectedAnnotations {
					if _, exists := pod.Annotations[annotation]; !exists {
						t.Logf(
							"Waiting for pod %s to have annotation %s",
							"user-baseline-violating-pod", annotation,
						)
						return false, nil
					}
				}

				// Verify specific annotation values
				subjectType := pod.Annotations["security.openshift.io/validated-scc-subject-type"]
				if subjectType != "user" {
					return false, fmt.Errorf(
						"expected subject type 'user', got '%s'",
						subjectType,
					)
				}

				sccName := pod.Annotations["openshift.io/scc"]
				if sccName != "privileged" {
					return false, fmt.Errorf(
						"expected SCC 'privileged', got '%s'",
						sccName,
					)
				}

				t.Logf("Pod has correct SCC annotations: subject-type=%s, scc=%s",
					subjectType, sccName)
				return true, nil
			},
		)
		if err != nil {
			t.Fatalf("Failed waiting for pod SCC annotations: %v", err)
		}

		t.Logf("Successfully created violating pod with correct SCC annotations")
	})

	t.Run("Remove pod security admission labels", func(t *testing.T) {
		// Alternative approaches for removing namespace labels:
		//
		// Method 1: Update with modified labels map (implemented below)
		// - Get namespace, modify labels map, update namespace
		//
		// Method 2: Using Strategic Merge Patch
		// patchData := `{"metadata":{"labels":{"pod-security.kubernetes.io/enforce":null}}}`
		// _, err := clientset.CoreV1().Namespaces().Patch(ctx, namespace,
		//     types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
		//
		// Method 3: Using JSON Patch
		// patchData := `[{"op":"remove","path":"/metadata/labels/pod-security.kubernetes.io~1enforce"}]`
		// _, err := clientset.CoreV1().Namespaces().Patch(ctx, namespace,
		//     types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
		//
		// Method 4: Using Apply Configuration (Server-Side Apply)
		// nsApply := applyconfiguration.Namespace(namespace).WithLabels(map[string]string{})
		// _, err := clientset.CoreV1().Namespaces().Apply(ctx, nsApply, metav1.ApplyOptions{
		//     FieldManager: "test-controller", Force: true})

		// Labels to remove
		labelsToRemove := []string{
			"pod-security.kubernetes.io/enforce",
			"pod-security.kubernetes.io/audit",
			"pod-security.kubernetes.io/warn",
			"pod-security.kubernetes.io/audit-version",
			"pod-security.kubernetes.io/warn-version",
			"pod-security.kubernetes.io/enforce-version",
		}

		// Get current namespace
		ns, err := clientset.CoreV1().Namespaces().Get(
			ctx,
			namespace,
			metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to get namespace: %v", err)
		}

		// Create a copy and remove PSA labels
		nsCopy := ns.DeepCopy()
		if nsCopy.Labels == nil {
			nsCopy.Labels = make(map[string]string)
		}

		removedLabels := []string{}
		for _, labelKey := range labelsToRemove {
			if _, exists := nsCopy.Labels[labelKey]; exists {
				delete(nsCopy.Labels, labelKey)
				removedLabels = append(removedLabels, labelKey)
			}
		}

		if len(removedLabels) == 0 {
			t.Logf("No PSA labels found to remove")
			return
		}

		// Update the namespace
		_, err = clientset.CoreV1().Namespaces().Update(
			ctx,
			nsCopy,
			metav1.UpdateOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to update namespace: %v", err)
		}

		t.Logf("Removed %d PSA labels: %v", len(removedLabels), removedLabels)

		// Verify labels were removed
		err = wait.PollUntilContextTimeout(
			ctx,
			pollInterval,
			pollTimeout,
			immediate,
			func(ctx context.Context) (bool, error) {
				updatedNS, err := clientset.CoreV1().Namespaces().Get(
					ctx,
					namespace,
					metav1.GetOptions{},
				)
				if err != nil {
					return false, err
				}

				// Check that all specified labels are gone
				for _, labelKey := range labelsToRemove {
					if _, exists := updatedNS.Labels[labelKey]; exists {
						t.Logf("Waiting for label %s to be removed", labelKey)
						return false, nil
					}
				}

				t.Logf("Successfully verified all PSA labels were removed")
				return true, nil
			},
		)

		if err != nil {
			t.Fatalf("Failed to verify label removal: %v", err)
		}
	})

	t.Run("TestDryRunEnforcement", func(t *testing.T) {
		// Create a warning-aware client to capture PSA warnings
		warningsHandler := &warningsHandler{}
		warningConfig := rest.CopyConfig(config)
		warningConfig.WarningHandler = warningsHandler

		warningClientset, err := kubernetes.NewForConfig(warningConfig)
		if err != nil {
			t.Fatalf("Failed to create warning-aware clientset: %v", err)
		}

		ns, err := clientset.CoreV1().Namespaces().Get(
			ctx,
			namespace,
			metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("Failed to get test namespace: %v", err)
		}

		// Create a copy with enforce=restricted label
		nsCopy := ns.DeepCopy()
		if nsCopy.Labels == nil {
			nsCopy.Labels = make(map[string]string)
		}
		nsCopy.Labels["pod-security.kubernetes.io/enforce"] = "restricted"

		// Clear any existing warnings
		warningsHandler.PopAll()

		// Attempt dry-run update with warning capture
		_, err = warningClientset.CoreV1().Namespaces().Update(
			ctx,
			nsCopy,
			metav1.UpdateOptions{DryRun: []string{"All"}},
		)

		// Capture warnings
		warnings := warningsHandler.PopAll()

		if err != nil {
			t.Logf("Dry-run with enforce=restricted returned error: %v", err)
		}

		if len(warnings) > 0 {
			t.Logf("Captured %d PSA warnings:", len(warnings))
			for i, warning := range warnings {
				t.Logf("  Warning %d: %s", i+1, warning)
			}

			// Verify we got PSA-related warnings
			foundPSAWarning := false
			for _, warning := range warnings {
				if strings.Contains(warning, "pod-security") ||
					strings.Contains(warning, "restricted") ||
					strings.Contains(warning, "baseline") {
					foundPSAWarning = true
					break
				}
			}

			if foundPSAWarning {
				t.Logf("✓ Successfully captured PSA-related warnings")
			} else {
				t.Logf("⚠ No PSA-related warnings found in captured warnings")
			}
		} else {
			t.Logf("No warnings captured during dry-run")
		}

		t.Logf("Dry-run enforcement test completed")
	})

	t.Run("WaitForViolationDetection", func(t *testing.T) {
		gvr := schema.GroupVersionResource{
			Group:    "config.openshift.io",
			Version:  "v1",
			Resource: "clusteroperators",
		}

		err := wait.PollUntilContextTimeout(
			ctx,
			pollInterval,
			pollTimeout,
			immediate,
			func(ctx context.Context) (bool, error) {
				co, err := dynamicClient.Resource(gvr).Get(
					ctx,
					"kube-apiserver",
					metav1.GetOptions{},
				)
				if err != nil {
					t.Logf("Warning: failed to get cluster operator: %v", err)
					return false, nil
				}

				// Check for PodSecurityUserSCCViolationConditionsDetected condition
				conditions, found, err := unstructured.NestedSlice(
					co.Object,
					"status",
					"conditions",
				)
				if err != nil || !found {
					return false, nil
				}

				for _, conditionRaw := range conditions {
					condition, ok := conditionRaw.(map[string]interface{})
					if !ok {
						continue
					}

					condType, _, _ := unstructured.NestedString(condition, "type")
					if condType == "PodSecurityUserSCCViolationConditionsDetected" {
						status, _, _ := unstructured.NestedString(condition, "status")
						message, _, _ := unstructured.NestedString(condition, "message")

						t.Logf("Found PodSecurityUserSCCViolationConditionsDetected condition: status=%s, message=%s",
							status, message)
						return true, nil
					}
				}

				t.Logf("Waiting for PodSecurityUserSCCViolationConditionsDetected condition...")
				return false, nil
			},
		)

		if err != nil {
			t.Fatalf("Timeout waiting for PodSecurityUserSCCViolationConditionsDetected condition: %v", err)
		}

		t.Logf("Successfully detected PSA user SCC violation condition")
	})
}
