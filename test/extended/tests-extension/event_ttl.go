package extended

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][FeatureGate:EventTTL] Event TTL Configuration", func() {
	var (
		kubeClient     *kubernetes.Clientset
		configClient   *configclient.Clientset
		operatorClient *operatorclientset.Clientset
		ctx            context.Context
	)

	g.BeforeEach(func() {
		ctx = context.TODO()
		kubeConfig, err := test.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kube config")

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create kube client")

		configClient, err = configclient.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create config client")

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create operator client")
	})

	// Loop to create separate test cases for each TTL value (5m, 10m, 15m)
	testValues := []int32{5, 10, 15}

	for _, ttlMinutes := range testValues {
		// Capture the variable for the closure
		ttl := ttlMinutes

		g.It(fmt.Sprintf("should configure and validate eventTTLMinutes=%dm [Disruptive][Slow][Suite:openshift/cluster-kube-apiserver-operator/conformance/serial]", ttl), func() {
			startTime := time.Now()
			g.By(fmt.Sprintf("=== Starting test for eventTTLMinutes=%d at %s ===", ttl, startTime.Format(time.RFC3339)))

			// Cleanup eventTTLMinutes configuration after test to ensure test isolation
			defer func() {
				g.By(fmt.Sprintf("\nCleaning up eventTTLMinutes=%d configuration", ttl))
				cleanupPatch := `{"spec":{"eventTTLMinutes":null}}`
				_, cleanupErr := operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, []byte(cleanupPatch), metav1.PatchOptions{})
				if cleanupErr != nil {
					g.By(fmt.Sprintf("  Warning: Failed to cleanup eventTTLMinutes: %v", cleanupErr))
				} else {
					g.By("  [OK] eventTTLMinutes configuration removed")

					// Wait for cluster to stabilize after configuration removal
					g.By("  Waiting for kube-apiserver rollout after cleanup...")
					rolloutErr := waitForAPIServerRollout(ctx, kubeClient, 25*time.Minute)
					if rolloutErr != nil {
						g.By(fmt.Sprintf("  Warning: Rollout did not complete after cleanup: %v", rolloutErr))
					} else {
						g.By("  [OK] Kube-apiserver rollout completed after cleanup")
					}
				}
			}()

			// Step 1: Check if EventTTL feature gate exists and get its status
			g.By("Step 1: Checking if EventTTL feature gate is present")

			featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred(), "failed to get feature gate")
			g.By(fmt.Sprintf("  Current FeatureSet: %s", featureGate.Spec.FeatureSet))

			// Check if EventTTL feature gate exists (enabled or disabled)
			foundFeature := false
			isEnabled := false

			for _, featureGateDetails := range featureGate.Status.FeatureGates {
				// Check enabled features
				for _, enabledFeature := range featureGateDetails.Enabled {
					if string(enabledFeature.Name) == "EventTTL" {
						foundFeature = true
						isEnabled = true
						g.By("[OK] EventTTL feature gate found and is already enabled")
						break
					}
				}
				// Check disabled features
				if !foundFeature {
					for _, disabledFeature := range featureGateDetails.Disabled {
						if string(disabledFeature.Name) == "EventTTL" {
							foundFeature = true
							isEnabled = false
							g.By("[OK] EventTTL feature gate found but is disabled")
							break
						}
					}
				}
				if foundFeature {
					break
				}
			}

			// If feature gate not found, skip the test
			if !foundFeature {
				g.Skip("EventTTL feature gate not found in this cluster version")
			}

			// Enable feature gate if not already enabled
			if !isEnabled {
				g.By("Step 1b: Enabling EventTTL feature gate...")
				enableStartTime := time.Now()
				g.By(fmt.Sprintf("  Enabling at: %s", enableStartTime.Format(time.RFC3339)))

				patchData := map[string]interface{}{
					"spec": map[string]interface{}{
						"featureSet": "CustomNoUpgrade",
						"customNoUpgrade": map[string]interface{}{
							"enabled": []string{"EventTTL"},
						},
					},
				}
				patchBytes, err := json.Marshal(patchData)
				o.Expect(err).NotTo(o.HaveOccurred())
				g.By(fmt.Sprintf("  Patch data: %s", string(patchBytes)))

				_, err = configClient.ConfigV1().FeatureGates().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
				o.Expect(err).NotTo(o.HaveOccurred())
				g.By("[OK] EventTTL feature gate patch applied - waiting for it to become enabled...")

				// Wait for feature gate to actually be enabled
				err = waitForFeatureGateEnabled(ctx, configClient, "EventTTL", 15*time.Minute)
				o.Expect(err).NotTo(o.HaveOccurred(), "EventTTL feature gate did not become enabled within timeout")

				// Wait for API server rollout after feature gate change
				g.By("  Waiting for kube-apiserver rollout after feature gate change...")
				err = waitForAPIServerRollout(ctx, kubeClient, 15*time.Minute)
				o.Expect(err).NotTo(o.HaveOccurred(), "API server did not complete rollout after feature gate change")

				g.By(fmt.Sprintf("  Feature gate enable took: %v", time.Since(enableStartTime)))
			}

			// Step 2: Configure eventTTLMinutes
			g.By(fmt.Sprintf("\nStep 2: Configuring eventTTLMinutes=%d in KubeAPIServer CR", ttl))
			configStartTime := time.Now()

			patchData := map[string]interface{}{
				"spec": map[string]interface{}{
					"eventTTLMinutes": ttl,
				},
			}
			patchBytes, err := json.Marshal(patchData)
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By(fmt.Sprintf("  Patch data: %s", string(patchBytes)))

			_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By(fmt.Sprintf("[OK] eventTTLMinutes=%d configured at %s", ttl, time.Now().Format(time.RFC3339)))

			// Step 3: Wait for rollout
			g.By("Step 3: Waiting for new revision to roll out (timeout: 20 minutes)...")
			rolloutStartTime := time.Now()
			g.By(fmt.Sprintf("  Rollout started at: %s", rolloutStartTime.Format(time.RFC3339)))

			err = waitForAPIServerRollout(ctx, kubeClient, 20*time.Minute)
			o.Expect(err).NotTo(o.HaveOccurred())
			rolloutDuration := time.Since(rolloutStartTime)
			g.By(fmt.Sprintf("[OK] New revision rolled out successfully in %v", rolloutDuration))
			g.By(fmt.Sprintf("  Configuration took: %v total", time.Since(configStartTime)))

			// Step 4: Verify configuration in the config file
			// Note: kube-apiserver loads arguments from config.yaml, not from pod.spec.args
			g.By(fmt.Sprintf("Step 4: Verifying event-ttl=%dm in kube-apiserver config", ttl))
			verifyEventTTLInConfigFile(ctx, kubeClient, ttl)
			g.By(fmt.Sprintf("[OK] eventTTLMinutes=%d verified in config file", ttl))

			// Step 5: Validate actual event expiration
			// IMPORTANT: We create a NEW event AFTER the configuration is applied.
			// The EventTTL feature only affects NEW events created after --event-ttl is set.
			// Existing events before the change keep their original TTL (default 3h).
			g.By(fmt.Sprintf("\nStep 5: Validating that events actually expire after %d minutes", ttl))

			// Wait a bit after rollout to ensure new TTL is fully propagated
			g.By("  Waiting 30 seconds for new TTL configuration to fully propagate...")
			time.Sleep(30 * time.Second)

			eventStartTime := time.Now()

			// Create a dedicated test namespace for cleaner isolation
			testNamespaceName := fmt.Sprintf("event-ttl-test-%dm-%d", ttl, time.Now().Unix())
			g.By(fmt.Sprintf("Creating test namespace: %s", testNamespaceName))
			testNs, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespaceName,
				},
			}, metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			testNamespace := testNs.Name
			g.By(fmt.Sprintf("[OK] Test namespace created: %s", testNamespace))

			// Ensure cleanup of test namespace
			defer func() {
				g.By(fmt.Sprintf("Cleaning up test namespace: %s", testNamespace))
				cleanupErr := kubeClient.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
				if cleanupErr != nil {
					g.By(fmt.Sprintf("  Warning: failed to delete test namespace: %v", cleanupErr))
				}
			}()

			eventName := fmt.Sprintf("apiload-event-%d", time.Now().Unix())

			g.By(fmt.Sprintf("Creating NEW test event: %s in namespace: %s", eventName, testNamespace))
			g.By("  (This event should expire after the configured TTL, not the default 3h)")
			g.By(fmt.Sprintf("  Event creation time: %s", eventStartTime.Format(time.RFC3339)))
			testEvent := &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      eventName,
					Namespace: testNamespace,
				},
				InvolvedObject: corev1.ObjectReference{
					Kind:      "Pod",
					Namespace: testNamespace,
					Name:      fmt.Sprintf("test-pod-%dm", ttl),
					UID:       types.UID(fmt.Sprintf("uid-%d", time.Now().Unix())),
				},
				Reason:         "EventTTLTest",
				Message:        fmt.Sprintf("Test event - should expire after %dm", ttl),
				Type:           corev1.EventTypeNormal,
				Source:         corev1.EventSource{Component: "event-ttl-test"},
				FirstTimestamp: metav1.Now(),
				LastTimestamp:  metav1.Now(),
				Count:          1,
			}

			createdEvent, err := kubeClient.CoreV1().Events(testNamespace).Create(ctx, testEvent, metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			creationTime := createdEvent.CreationTimestamp.Time
			g.By(fmt.Sprintf("[OK] Event created at: %s", creationTime.Format(time.RFC3339)))

			// Verify event exists
			event, err := kubeClient.CoreV1().Events(testNamespace).Get(ctx, eventName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By(fmt.Sprintf("[OK] Event confirmed to exist (UID: %s)", event.UID))
			g.By(fmt.Sprintf("  Event CreationTimestamp: %s", event.CreationTimestamp.Format(time.RFC3339)))

			// NOTE: We create this event AFTER setting eventTTLMinutes, so it should have the configured TTL.
			// Events created BEFORE the config change retain their original 3h TTL.
			// The etcd lease would show TTL(%ds) for this event, but we verify by waiting for deletion instead.
			g.By(fmt.Sprintf("  This NEW event should expire after %dm (not the default 3h)", ttl))

			// Wait for TTL + buffer (add extra time for GC intervals and clock skew)
			bufferMinutes := int32(3) // Increased buffer from 2 to 3 minutes
			waitDuration := time.Duration(ttl+bufferMinutes) * time.Minute
			expectedExpirationTime := creationTime.Add(waitDuration)
			g.By(fmt.Sprintf("Waiting %d minutes for event to expire (expected expiration: %s)...",
				int(ttl+bufferMinutes), expectedExpirationTime.Format(time.RFC3339)))

			// Log progress every minute
			ticker := time.NewTicker(1 * time.Minute)
			done := make(chan bool, 1) // Buffered channel to prevent goroutine leak
			elapsed := 0

			// Ensure ticker is always stopped to prevent goroutine leak
			defer func() {
				ticker.Stop()
				select {
				case done <- true:
				default:
				}
			}()

			go func() {
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						elapsed++
						g.By(fmt.Sprintf("  ... %d/%d minutes elapsed", elapsed, int(ttl+bufferMinutes)))
					}
				}
			}()

			time.Sleep(waitDuration)

			// Verify event is deleted
			actualExpirationTime := time.Now()
			_, err = kubeClient.CoreV1().Events(testNamespace).Get(ctx, eventName, metav1.GetOptions{})
			o.Expect(err).To(o.HaveOccurred(), "event should be deleted after TTL")
			o.Expect(err.Error()).To(o.ContainSubstring("not found"), "event should return 'not found' error")

			actualTTL := actualExpirationTime.Sub(creationTime)
			g.By(fmt.Sprintf("[OK] Event expired and deleted after approximately %v", actualTTL.Round(time.Minute)))
			g.By(fmt.Sprintf("  Expected TTL: %dm, Actual TTL: %v", ttl, actualTTL.Round(time.Minute)))

			totalTestDuration := time.Since(startTime)
			g.By(fmt.Sprintf("\n[SUCCESS] All steps completed successfully for eventTTLMinutes=%d", ttl))
			g.By(fmt.Sprintf("  Total test duration: %v", totalTestDuration))
		})
	}
})

// Helper functions

func waitForFeatureGateEnabled(ctx context.Context, configClient *configclient.Clientset, featureName string, timeout time.Duration) error {
	g.By(fmt.Sprintf("Waiting for feature gate %s to be enabled (timeout: %v)", featureName, timeout))
	attempt := 0

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, false, func(cxt context.Context) (bool, error) {
		attempt++
		fg, err := configClient.ConfigV1().FeatureGates().Get(cxt, "cluster", metav1.GetOptions{})
		if err != nil {
			g.By(fmt.Sprintf("  [Attempt %d] Error getting feature gate: %v", attempt, err))
			return false, nil
		}

		for _, fgDetails := range fg.Status.FeatureGates {
			for _, enabled := range fgDetails.Enabled {
				if string(enabled.Name) == featureName {
					g.By(fmt.Sprintf("  [Attempt %d] [OK] Feature gate %s is enabled", attempt, featureName))
					return true, nil
				}
			}
		}

		if attempt%6 == 0 { // Log every minute
			g.By(fmt.Sprintf("  [Attempt %d] Feature gate %s not yet enabled, waiting...", attempt, featureName))
		}
		return false, nil
	})
}

func waitForAPIServerRollout(ctx context.Context, kubeClient *kubernetes.Clientset, timeout time.Duration) error {
	// First, get the current revision and pod creation times BEFORE we start waiting
	initialPods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=openshift-kube-apiserver,apiserver=true",
	})
	if err != nil {
		g.By(fmt.Sprintf("  Warning: Could not get initial pods: %v", err))
	}

	// Track the oldest pod creation time - we need to see pods newer than this
	var oldestPodTime time.Time
	initialRevision := ""
	if initialPods != nil && len(initialPods.Items) > 0 {
		oldestPodTime = initialPods.Items[0].CreationTimestamp.Time
		for _, pod := range initialPods.Items {
			if pod.CreationTimestamp.Time.Before(oldestPodTime) {
				oldestPodTime = pod.CreationTimestamp.Time
			}
			// Get the revision from labels
			if rev, ok := pod.Labels["revision"]; ok && initialRevision == "" {
				initialRevision = rev
			}
		}
		g.By(fmt.Sprintf("  Initial state: %d pods, oldest created at %s, initial revision: %s",
			len(initialPods.Items), oldestPodTime.Format(time.RFC3339), initialRevision))
	}

	attempt := 0
	lastPodCount := 0
	lastNotRunningCount := 0
	rolloutStartTime := time.Now()

	return wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, false, func(cxt context.Context) (bool, error) {
		attempt++
		pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(cxt, metav1.ListOptions{
			LabelSelector: "app=openshift-kube-apiserver,apiserver=true",
		})
		if err != nil {
			g.By(fmt.Sprintf("  [Attempt %d] Error listing pods: %v", attempt, err))
			return false, nil
		}

		if len(pods.Items) == 0 {
			g.By(fmt.Sprintf("  [Attempt %d] No kube-apiserver pods found yet", attempt))
			return false, nil
		}

		// Count pods and check if we have new pods (created after rollout started)
		notRunningCount := 0
		newPodsCount := 0
		runningNewPodsCount := 0
		var notRunningPods []string
		var currentRevision string

		for _, pod := range pods.Items {
			// Check if this is a new pod (created after we started waiting for rollout)
			isNewPod := pod.CreationTimestamp.Time.After(rolloutStartTime)

			if pod.Status.Phase != corev1.PodRunning {
				notRunningCount++
				notRunningPods = append(notRunningPods, fmt.Sprintf("%s (%s)", pod.Name, pod.Status.Phase))
			}

			if isNewPod {
				newPodsCount++
				if pod.Status.Phase == corev1.PodRunning {
					runningNewPodsCount++
				}
			}

			// Track current revision
			if rev, ok := pod.Labels["revision"]; ok && currentRevision == "" {
				currentRevision = rev
			}
		}

		// We need ALL pods to be:
		// 1. Running
		// 2. Created after rollout started (new pods with new configuration)
		// We expect 3 pods for a typical control plane
		expectedPodCount := 3
		allPodsNewAndRunning := (newPodsCount == expectedPodCount && runningNewPodsCount == expectedPodCount)

		// Log only when state changes or every 4th attempt (1 minute)
		if notRunningCount != lastNotRunningCount || len(pods.Items) != lastPodCount || attempt%4 == 0 {
			if notRunningCount > 0 {
				g.By(fmt.Sprintf("  [Attempt %d] %d/%d pods running. Not running: %v. New pods: %d/%d running",
					attempt, len(pods.Items)-notRunningCount, len(pods.Items), notRunningPods, runningNewPodsCount, newPodsCount))
			} else {
				g.By(fmt.Sprintf("  [Attempt %d] All %d pods are running. New pods: %d/%d. Revision: %s",
					attempt, len(pods.Items), runningNewPodsCount, newPodsCount, currentRevision))
			}
			lastPodCount = len(pods.Items)
			lastNotRunningCount = notRunningCount
		}

		// Success: all expected pods are new and running
		return allPodsNewAndRunning, nil
	})
}

func verifyEventTTLInConfigFile(ctx context.Context, kubeClient *kubernetes.Clientset, expectedMinutes int32) {
	g.By("Checking event-ttl in pod config file")

	// Get a running kube-apiserver pod
	pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=openshift-kube-apiserver,apiserver=true",
	})
	o.Expect(err).NotTo(o.HaveOccurred())

	var targetPod *corev1.Pod
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			targetPod = &pods.Items[i]
			break
		}
	}
	o.Expect(targetPod).NotTo(o.BeNil(), "no running kube-apiserver pod found")

	g.By(fmt.Sprintf("  Checking config file in pod: %s", targetPod.Name))

	// Read the config file using kubectl exec
	// The config is at /etc/kubernetes/static-pod-resources/configmaps/config/config.yaml
	configPath := "/etc/kubernetes/static-pod-resources/configmaps/config/config.yaml"

	// Use pod logs API to verify - alternatively we can check the ConfigMap directly
	// Let's check the ConfigMap which is easier and doesn't require exec
	configMapName := "config"
	configMap, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, configMapName, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to get config ConfigMap")

	configData, found := configMap.Data["config.yaml"]
	o.Expect(found).To(o.BeTrue(), "config.yaml not found in ConfigMap")

	// Parse the config and look for event-ttl in apiServerArguments
	expectedTTL := fmt.Sprintf("%dm", expectedMinutes)

	// Check if event-ttl is in the config
	hasEventTTL := strings.Contains(configData, "event-ttl")
	if !hasEventTTL {
		// event-ttl must be present in the config
		o.Expect(hasEventTTL).To(o.BeTrue(),
			fmt.Sprintf("event-ttl not found in ConfigMap %s (path in pod: %s)", configMapName, configPath))
	}

	// Further verify it has the correct value
	hasCorrectValue := strings.Contains(configData, fmt.Sprintf("- %s", expectedTTL)) ||
		strings.Contains(configData, fmt.Sprintf("\"%s\"", expectedTTL)) ||
		strings.Contains(configData, fmt.Sprintf("'%s'", expectedTTL))

	if hasCorrectValue {
		g.By(fmt.Sprintf("  [OK] Found event-ttl=%s in config.yaml", expectedTTL))
	} else {
		// Log what we found for debugging before failing
		lines := strings.Split(configData, "\n")
		for i, line := range lines {
			if strings.Contains(line, "event-ttl") {
				// Show context around the line
				start := i - 2
				if start < 0 {
					start = 0
				}
				end := i + 3
				if end > len(lines) {
					end = len(lines)
				}
				context := strings.Join(lines[start:end], "\n")
				g.By(fmt.Sprintf("  Found event-ttl in config:\n%s", context))
			}
		}
		o.Expect(hasCorrectValue).To(o.BeTrue(),
			fmt.Sprintf("event-ttl found in config but value doesn't match expected %s", expectedTTL))
	}
}
