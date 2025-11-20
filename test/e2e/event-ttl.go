package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	ote "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	libgotest "github.com/openshift/library-go/test/library"
	"github.com/openshift/library-go/test/library/apiserver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][FeatureGate:EventTTL] Event TTL Configuration", g.Ordered, func() {
	var (
		kubeClient              *kubernetes.Clientset
		configClient            *configclient.Clientset
		operatorClient          *operatorclientset.Clientset
		ctx                     context.Context
		originalFeatureSet      string
		originalEnabledFeatures []string
		featureGateWasModified  bool
	)

	// Enable EventTTL feature gate once before all tests
	g.BeforeAll(func() {
		// Initialize clients first
		ctx = context.TODO()

		// Redirect stdout temporarily to prevent "Found configuration..." from polluting JSON output
		oldStdout := os.Stdout
		devNull, _ := os.Open(os.DevNull)
		os.Stdout = devNull

		kubeConfig, err := libgotest.NewClientConfigForTest()

		// Restore stdout immediately
		os.Stdout = oldStdout
		devNull.Close()

		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kube config")

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create kube client")

		configClient, err = configclient.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create config client")

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create operator client")

		g.By("=== Setup: Checking and enabling EventTTL feature gate ===")

		featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get feature gate")

		// Save original state
		originalFeatureSet = string(featureGate.Spec.FeatureSet)
		if featureGate.Spec.CustomNoUpgrade != nil && featureGate.Spec.CustomNoUpgrade.Enabled != nil {
			originalEnabledFeatures = make([]string, len(featureGate.Spec.CustomNoUpgrade.Enabled))
			for i, f := range featureGate.Spec.CustomNoUpgrade.Enabled {
				originalEnabledFeatures[i] = string(f)
			}
		}
		g.By(fmt.Sprintf("  Original FeatureSet: %s, Enabled features: %d", originalFeatureSet, len(originalEnabledFeatures)))

		// Check if EventTTL feature gate exists and is enabled
		foundFeature := false
		isEnabled := false
		for _, featureGateDetails := range featureGate.Status.FeatureGates {
			for _, enabledFeature := range featureGateDetails.Enabled {
				if string(enabledFeature.Name) == "EventTTL" {
					foundFeature = true
					isEnabled = true
					g.By("  [OK] EventTTL feature gate is already enabled")
					break
				}
			}
			if !foundFeature {
				for _, disabledFeature := range featureGateDetails.Disabled {
					if string(disabledFeature.Name) == "EventTTL" {
						foundFeature = true
						isEnabled = false
						g.By("  EventTTL feature gate found but is disabled")
						break
					}
				}
			}
			if foundFeature {
				break
			}
		}

		if !foundFeature {
			g.Skip("EventTTL feature gate not found in this cluster version")
		}

		// Enable feature gate if not already enabled
		if !isEnabled {
			g.By("  Enabling EventTTL feature gate...")
			enableStartTime := time.Now()

			// Read-modify-write to avoid clobbering existing feature gates
			currentFG, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred(), "failed to get current feature gate")

			// Collect existing enabled features
			existingFeatures := make(map[string]bool)
			// If switching from a predefined feature set, preserve all currently enabled features
			if originalFeatureSet != "CustomNoUpgrade" {
				for _, fgStatus := range featureGate.Status.FeatureGates {
					for _, enabled := range fgStatus.Enabled {
						existingFeatures[string(enabled.Name)] = true
					}
				}
			}
			// Also include any features already in CustomNoUpgrade.Enabled
			if currentFG.Spec.CustomNoUpgrade != nil && currentFG.Spec.CustomNoUpgrade.Enabled != nil {
				for _, f := range currentFG.Spec.CustomNoUpgrade.Enabled {
					existingFeatures[string(f)] = true
				}
			}

			// Add EventTTL if not present
			if !existingFeatures["EventTTL"] {
				existingFeatures["EventTTL"] = true
				g.By(fmt.Sprintf("  Adding EventTTL to existing %d feature gates", len(existingFeatures)-1))
			}

			// Convert to slice
			enabledSlice := make([]string, 0, len(existingFeatures))
			for f := range existingFeatures {
				enabledSlice = append(enabledSlice, f)
			}

			// Prepare patch
			patchData := map[string]interface{}{
				"spec": map[string]interface{}{
					"featureSet": "CustomNoUpgrade",
					"customNoUpgrade": map[string]interface{}{
						"enabled": enabledSlice,
					},
				},
			}
			fmt.Fprintf(os.Stderr, "[DEBUG BeforeAll] Marshaling feature gate enable patch: %+v\n", patchData)
			patchBytes, err := json.Marshal(patchData)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR BeforeAll] json.Marshal failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[DEBUG BeforeAll] Marshal success: %s\n", string(patchBytes))
			}
			o.Expect(err).NotTo(o.HaveOccurred())

			_, err = configClient.ConfigV1().FeatureGates().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			featureGateWasModified = true
			g.By("  [OK] EventTTL feature gate patch applied")

			// Wait for feature gate to actually be enabled
			err = waitForFeatureGateEnabled(ctx, configClient, "EventTTL", 20*time.Minute)
			o.Expect(err).NotTo(o.HaveOccurred(), "EventTTL feature gate did not become enabled within timeout")

			// Wait for API server rollout after feature gate change
			g.By("  Waiting for kube-apiserver rollout after feature gate change...")
			err = waitForAPIServerRollout(ctx, kubeClient, 20*time.Minute)
			o.Expect(err).NotTo(o.HaveOccurred(), "API server did not complete rollout after feature gate change")

			// Ensure cluster is stable before running tests
			g.By("  Waiting for kube-apiserver to stabilize on the same revision...")
			podClient := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace)
			err = apiserver.WaitForAPIServerToStabilizeOnTheSameRevision(&ginkgoLogger{}, podClient)
			o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize on the same revision")

			g.By(fmt.Sprintf("  [OK] Feature gate enable took: %v", time.Since(enableStartTime)))
		}
	})

	// Restore feature gate after all tests
	g.AfterAll(func() {
		if !featureGateWasModified {
			g.By("=== Cleanup: Feature gate was not modified, skipping restore ===")
			return
		}

		g.By("=== Cleanup: Restoring original feature gate configuration ===")
		restorePatch := map[string]interface{}{
			"spec": map[string]interface{}{
				"featureSet": originalFeatureSet,
			},
		}
		if originalFeatureSet == "CustomNoUpgrade" && len(originalEnabledFeatures) > 0 {
			restorePatch["spec"].(map[string]interface{})["customNoUpgrade"] = map[string]interface{}{
				"enabled": originalEnabledFeatures,
			}
		}
		if originalFeatureSet != "CustomNoUpgrade" {
			restorePatch["spec"].(map[string]interface{})["customNoUpgrade"] = nil
		}
		fmt.Fprintf(os.Stderr, "[DEBUG AfterAll] Marshaling feature gate restore patch: %+v\n", restorePatch)
		restorePatchBytes, err := json.Marshal(restorePatch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR AfterAll] json.Marshal failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG AfterAll] Marshal success: %s\n", string(restorePatchBytes))
		}
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to marshal feature gate restore patch")
		_, err = configClient.ConfigV1().FeatureGates().Patch(ctx, "cluster", types.MergePatchType, restorePatchBytes, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to restore feature gate configuration")
		{
			g.By("  [OK] Feature gate configuration restored")
			// Wait for cluster to stabilize
			g.By("  Waiting for kube-apiserver rollout after restore...")
			rolloutErr := waitForAPIServerRollout(ctx, kubeClient, 25*time.Minute)
			o.Expect(rolloutErr).NotTo(o.HaveOccurred(), "API server did not complete rollout after restore")

			// Ensure cluster is stable after restore
			g.By("  Waiting for kube-apiserver to stabilize on the same revision...")
			podClient := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace)
			stabilizeErr := apiserver.WaitForAPIServerToStabilizeOnTheSameRevision(&ginkgoLogger{}, podClient)
			o.Expect(stabilizeErr).NotTo(o.HaveOccurred(), "API server did not stabilize on the same revision after restore")
			g.By("  [OK] Kube-apiserver stabilized on the same revision")
		}
	})

	// Loop to create separate test cases for each TTL value (5m, 10m, 15m)
	testValues := []int32{5, 10, 15}
	for _, ttlMinutes := range testValues {
		ttl := ttlMinutes

		g.It(fmt.Sprintf("should configure and validate eventTTLMinutes=%dm [Timeout:60m][Slow][Serial][OTP]", ttl), ote.Informing(), func() {
			startTime := time.Now()
			g.By(fmt.Sprintf("=== Starting test for eventTTLMinutes=%d at %s ===", ttl, startTime.Format(time.RFC3339)))

			// Capture original eventTTLMinutes value before test
			// Note: EventTTLMinutes is int32 with omitempty - if not set, it returns 0
			currentCfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get current KubeAPIServer configuration")
			originalEventTTL := currentCfg.Spec.EventTTLMinutes
			if originalEventTTL == 0 {
				g.By("  Current eventTTLMinutes: <not set>")
			} else {
				g.By(fmt.Sprintf("  Current eventTTLMinutes: %d", originalEventTTL))
			}

			// Cleanup eventTTLMinutes configuration after test
			defer func() {
				g.By(fmt.Sprintf("\nCleaning up eventTTLMinutes=%d configuration", ttl))
				restore := map[string]interface{}{"spec": map[string]interface{}{}}
				if originalEventTTL == 0 {
					// Field was not set originally (zero value), so remove it
					restore["spec"].(map[string]interface{})["eventTTLMinutes"] = nil
					g.By("  Restoring eventTTLMinutes=null (was unset)")
				} else {
					// Field was set to a specific value, restore it
					restore["spec"].(map[string]interface{})["eventTTLMinutes"] = originalEventTTL
					g.By(fmt.Sprintf("  Restoring original eventTTLMinutes=%d", originalEventTTL))
				}
				fmt.Fprintf(os.Stderr, "[DEBUG Test Cleanup] Marshaling eventTTLMinutes restore patch: %+v\n", restore)
				restoreBytes, marshalErr := json.Marshal(restore)
				if marshalErr != nil {
					fmt.Fprintf(os.Stderr, "[ERROR Test Cleanup] json.Marshal failed: %v\n", marshalErr)
				} else {
					fmt.Fprintf(os.Stderr, "[DEBUG Test Cleanup] Marshal success: %s\n", string(restoreBytes))
				}
				o.Expect(marshalErr).NotTo(o.HaveOccurred(), "Failed to marshal restore patch")
				_, cleanupErr := operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, restoreBytes, metav1.PatchOptions{})
				o.Expect(cleanupErr).NotTo(o.HaveOccurred(), "Failed to cleanup eventTTLMinutes configuration")

				g.By("  [OK] eventTTLMinutes configuration restored")
				// Only wait for rollout if we're restoring to a non-zero value
				// Unsetting eventTTLMinutes (setting to null) may not trigger a rollout
				if originalEventTTL != 0 {
					g.By("  Waiting for kube-apiserver rollout after cleanup...")
					rolloutErr := waitForAPIServerRollout(ctx, kubeClient, 20*time.Minute)
					o.Expect(rolloutErr).NotTo(o.HaveOccurred(), "API server did not complete rollout after cleanup")
					g.By("  [OK] Kube-apiserver rollout completed after cleanup")
				} else {
					g.By("  Skipping rollout wait (unsetting eventTTLMinutes may not trigger rollout)")
					// Give it a few seconds for the operator to process the change
					time.Sleep(5 * time.Second)
				}
			}()

			// Step 1: Configure eventTTLMinutes
			g.By(fmt.Sprintf("Step 1: Configuring eventTTLMinutes=%d in KubeAPIServer CR", ttl))
			configStartTime := time.Now()

			patchData := map[string]interface{}{
				"spec": map[string]interface{}{
					"eventTTLMinutes": ttl,
				},
			}
			fmt.Fprintf(os.Stderr, "[DEBUG Test Config] Marshaling eventTTLMinutes=%d patch: %+v\n", ttl, patchData)
			patchBytes, err := json.Marshal(patchData)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR Test Config] json.Marshal failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[DEBUG Test Config] Marshal success: %s\n", string(patchBytes))
			}
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By(fmt.Sprintf("  Patch data: %s", string(patchBytes)))

			_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By(fmt.Sprintf("[OK] eventTTLMinutes=%d patch applied at %s", ttl, time.Now().Format(time.RFC3339)))

			// Verify the CR was actually updated
			updatedCfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(updatedCfg.Spec.EventTTLMinutes).To(o.Equal(ttl), "CR spec.eventTTLMinutes should be updated")
			g.By(fmt.Sprintf("  [VERIFIED] KubeAPIServer CR now has eventTTLMinutes=%d", updatedCfg.Spec.EventTTLMinutes))

			// Step 2: Wait for rollout
			g.By("Step 2: Waiting for new revision to roll out (timeout: 20 minutes)...")
			rolloutStartTime := time.Now()
			g.By(fmt.Sprintf("  Rollout started at: %s", rolloutStartTime.Format(time.RFC3339)))

			err = waitForAPIServerRollout(ctx, kubeClient, 20*time.Minute)
			o.Expect(err).NotTo(o.HaveOccurred())
			rolloutDuration := time.Since(rolloutStartTime)
			g.By(fmt.Sprintf("[OK] New revision rolled out successfully in %v", rolloutDuration))
			g.By(fmt.Sprintf("  Configuration took: %v total", time.Since(configStartTime)))

			// Step 3: Verify configuration in the config file
			// Note: kube-apiserver loads arguments from config.yaml, not from pod.spec.args
			g.By(fmt.Sprintf("Step 3: Verifying event-ttl=%dm in kube-apiserver config", ttl))
			verifyEventTTLInConfigFile(ctx, kubeClient, ttl)
			g.By(fmt.Sprintf("[OK] eventTTLMinutes=%d verified in config file", ttl))

			// Step 4: Validate actual event expiration
			// IMPORTANT: We create a NEW event AFTER the configuration is applied.
			// The EventTTL feature only affects NEW events created after --event-ttl is set.
			// Existing events before the change keep their original TTL (default 3h).
			g.By(fmt.Sprintf("\nStep 4: Validating that events actually expire after %d minutes", ttl))

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
				o.Expect(cleanupErr).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to delete test namespace %s", testNamespace))
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

// TODO: Move this function to https://github.com/openshift/library-go/tree/master/test as a common utility
// for waiting for API server rollouts across multiple operator repositories.
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
		// Derive expected count from current pod list (supports single-node and multi-node deployments)
		expectedPodCount := len(pods.Items)
		allPodsNewAndRunning := newPodsCount == expectedPodCount && runningNewPodsCount == expectedPodCount

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
	expectedTTL := fmt.Sprintf("%dm", expectedMinutes)

	// Poll for the config to have the correct value (retry for up to 2 minutes)
	// This handles the race condition where pods might be running but ConfigMap not yet updated
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, false, func(cxt context.Context) (bool, error) {
		// Get a running kube-apiserver pod
		pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(cxt, metav1.ListOptions{
			LabelSelector: "app=openshift-kube-apiserver,apiserver=true",
		})
		if err != nil {
			g.By(fmt.Sprintf("  Warning: Failed to list pods: %v", err))
			return false, nil
		}

		var targetPod *corev1.Pod
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				targetPod = &pods.Items[i]
				break
			}
		}
		if targetPod == nil {
			g.By("  Warning: No running kube-apiserver pod found, retrying...")
			return false, nil
		}

		g.By(fmt.Sprintf("  Checking config file in pod: %s", targetPod.Name))

		// Check the ConfigMap
		configMapName := "config"
		configMap, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(cxt, configMapName, metav1.GetOptions{})
		if err != nil {
			g.By(fmt.Sprintf("  Warning: Failed to get ConfigMap: %v", err))
			return false, nil
		}

		configData, found := configMap.Data["config.yaml"]
		if !found {
			g.By("  Warning: config.yaml not found in ConfigMap, retrying...")
			return false, nil
		}

		// Check if event-ttl is in the config
		hasEventTTL := strings.Contains(configData, "event-ttl")
		if !hasEventTTL {
			g.By("  Warning: event-ttl not found in config, retrying...")
			return false, nil
		}

		// Check if it has the correct value
		hasCorrectValue := strings.Contains(configData, fmt.Sprintf("\"event-ttl\":[\"%s\"]", expectedTTL)) ||
			strings.Contains(configData, fmt.Sprintf("- %s", expectedTTL)) ||
			strings.Contains(configData, fmt.Sprintf("\"%s\"", expectedTTL)) ||
			strings.Contains(configData, fmt.Sprintf("'%s'", expectedTTL))

		if hasCorrectValue {
			g.By(fmt.Sprintf("  [OK] Found event-ttl=%s in config.yaml", expectedTTL))
			return true, nil
		}

		// Log what we found for debugging
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
				g.By(fmt.Sprintf("  Found event-ttl with wrong value, expected %s:\n%s", expectedTTL, context))
				g.By("  Retrying in 5 seconds...")
				break
			}
		}
		return false, nil
	})

	o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to verify event-ttl=%s in config within timeout", expectedTTL))
}

// ginkgoLogger is a simple adapter to satisfy library.LoggingT interface
type ginkgoLogger struct{}

func (l *ginkgoLogger) Logf(format string, args ...interface{}) {
	g.By(fmt.Sprintf(format, args...))
}
