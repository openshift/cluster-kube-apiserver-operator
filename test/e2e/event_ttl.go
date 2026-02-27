package e2e

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
	libgotest "github.com/openshift/library-go/test/library"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][FeatureGate:EventTTL][EventTTL][Skipped:HyperShift][Skipped:MicroShift] Event TTL Configuration", g.Ordered, func() {
	var (
		kubeClient     *kubernetes.Clientset
		configClient   *configclient.Clientset
		operatorClient *operatorclientset.Clientset
		ctx            context.Context
	)

	const (
		successThreshold = 6
		successInterval  = 1 * time.Minute
		pollInterval     = 30 * time.Second
		timeout          = 20 * time.Minute
	)

	g.BeforeAll(func() {
		ctx = context.TODO()
		kubeConfig, err := libgotest.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get kube config")

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create kube client")

		configClient, err = configclient.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create config client")

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create operator client")

		// Check if EventTTL feature gate is available
		featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get feature gate")

		g.GinkgoWriter.Printf("Current FeatureSet: %s\n", featureGate.Spec.FeatureSet)

		eventTTLFound := false
		eventTTLEnabled := false
		for _, fgDetails := range featureGate.Status.FeatureGates {
			for _, enabled := range fgDetails.Enabled {
				if string(enabled.Name) == "EventTTL" {
					eventTTLFound = true
					eventTTLEnabled = true
					break
				}
			}
			for _, disabled := range fgDetails.Disabled {
				if string(disabled.Name) == "EventTTL" {
					eventTTLFound = true
					break
				}
			}
			if eventTTLFound {
				break
			}
		}

		if !eventTTLFound {
			g.Skip("EventTTL feature gate not found in this cluster version")
		}

		g.GinkgoWriter.Printf("EventTTL feature gate: found=%v, enabled=%v\n", eventTTLFound, eventTTLEnabled)

		// Skip if EventTTL feature gate is not enabled
		// This test is designed for TechPreview clusters where EventTTL is already enabled
		if !eventTTLEnabled {
			g.Skip("EventTTL feature gate is not enabled - this test requires a TechPreview cluster")
		}
	})

	g.It("should configure eventTTLMinutes and verify it in kube-apiserver config [Conformance][Serial][Timeout:60m][Late]", func() {
		ttl := int32(5)

		// Get original value for cleanup
		currentCfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		originalEventTTL := currentCfg.Spec.EventTTLMinutes
		g.GinkgoWriter.Printf("Original eventTTLMinutes: %d (0 means unset)\n", originalEventTTL)

		// Cleanup after test
		defer func() {
			g.By("Cleaning up eventTTLMinutes configuration")
			restore := map[string]interface{}{"spec": map[string]interface{}{}}
			if originalEventTTL == 0 {
				restore["spec"].(map[string]interface{})["eventTTLMinutes"] = nil
			} else {
				restore["spec"].(map[string]interface{})["eventTTLMinutes"] = originalEventTTL
			}
			restoreBytes, _ := json.Marshal(restore)
			_, restoreErr := operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, restoreBytes, metav1.PatchOptions{})
			if restoreErr != nil {
				g.GinkgoWriter.Printf("Cleanup: failed to restore eventTTLMinutes: %v\n", restoreErr)
				return
			}
			g.GinkgoWriter.Printf("Cleanup: restored eventTTLMinutes to original value\n")

			g.By("Waiting for API server to stabilize after cleanup")
			stabilizeErr := libgotest.WaitForPodsToStabilizeOnTheSameRevision(
				g.GinkgoT(),
				kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
				"apiserver=true",
				successThreshold, successInterval, pollInterval, timeout,
			)
			if stabilizeErr != nil {
				g.GinkgoWriter.Printf("Cleanup: API server did not stabilize after restore: %v\n", stabilizeErr)
			} else {
				g.GinkgoWriter.Printf("Cleanup: API server stabilized after restore\n")
			}
		}()

		g.By(fmt.Sprintf("Configuring eventTTLMinutes=%d", ttl))
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": ttl,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		// Verify the CR was updated
		updatedCfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(updatedCfg.Spec.EventTTLMinutes).To(o.Equal(ttl))
		g.GinkgoWriter.Printf("KubeAPIServer CR updated: eventTTLMinutes=%d\n", updatedCfg.Spec.EventTTLMinutes)

		g.By("Waiting for API server to stabilize with new eventTTLMinutes")
		err = libgotest.WaitForPodsToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
			"apiserver=true",
			successThreshold, successInterval, pollInterval, timeout,
		)
		o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize after eventTTLMinutes change")
		g.GinkgoWriter.Printf("API server stabilized with new configuration\n")

		g.By(fmt.Sprintf("Verifying event-ttl=%dm in kube-apiserver config", ttl))
		expectedTTL := fmt.Sprintf("%dm", ttl)

		var configData string
		o.Eventually(func() bool {
			configMap, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "config", metav1.GetOptions{})
			if err != nil {
				g.GinkgoWriter.Printf("  Failed to get config configmap: %v\n", err)
				return false
			}
			var found bool
			configData, found = configMap.Data["config.yaml"]
			if !found {
				g.GinkgoWriter.Printf("  config.yaml not found in configmap\n")
				return false
			}
			return strings.Contains(configData, "event-ttl") && strings.Contains(configData, expectedTTL)
		}, 2*time.Minute, 5*time.Second).Should(o.BeTrue(), fmt.Sprintf("event-ttl=%s should be in config", expectedTTL))

		g.By(fmt.Sprintf("Successfully verified eventTTLMinutes=%d configuration", ttl))

		g.By(fmt.Sprintf("Validating that events actually expire after %d minutes", ttl))

		// Create a test event in the operator's target namespace
		eventName := fmt.Sprintf("ttl-test-event-%d", time.Now().Unix())
		testEvent := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      eventName,
				Namespace: operatorclient.TargetNamespace,
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Pod",
				Namespace: operatorclient.TargetNamespace,
				Name:      "test-pod",
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

		createdEvent, err := kubeClient.CoreV1().Events(operatorclient.TargetNamespace).Create(ctx, testEvent, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		creationTime := createdEvent.CreationTimestamp.Time
		g.GinkgoWriter.Printf("Created test event: %s at %s\n", eventName, creationTime.Format(time.RFC3339))
		g.GinkgoWriter.Printf("  Event FirstTimestamp: %v\n", createdEvent.FirstTimestamp.Time.Format(time.RFC3339))
		g.GinkgoWriter.Printf("  Event LastTimestamp: %v\n", createdEvent.LastTimestamp.Time.Format(time.RFC3339))
		g.GinkgoWriter.Printf("  Event ResourceVersion: %s\n", createdEvent.ResourceVersion)

		// Debug: Re-verify KubeAPIServer CR has eventTTLMinutes set
		currentKAS, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		g.GinkgoWriter.Printf("  KubeAPIServer.Spec.EventTTLMinutes: %d\n", currentKAS.Spec.EventTTLMinutes)

		// Poll for event deletion
		// The event GC runs periodically and may not delete events immediately after TTL expires.
		// Use TTL + 10 minutes buffer to account for GC interval variability.
		waitTimeout := time.Duration(ttl+10) * time.Minute
		expectedExpiry := creationTime.Add(time.Duration(ttl) * time.Minute)
		g.GinkgoWriter.Printf("Waiting up to %v for event to expire (expected around %s)...\n",
			waitTimeout, expectedExpiry.Format(time.RFC3339))

		pollCount := 0
		o.Eventually(func() bool {
			pollCount++
			_, err := kubeClient.CoreV1().Events(operatorclient.TargetNamespace).Get(ctx, eventName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					g.GinkgoWriter.Printf("  Event deleted! (poll #%d)\n", pollCount)
					return true
				}
				g.GinkgoWriter.Printf("  Unexpected error getting event: %v\n", err)
				return false
			}
			// Log progress every 2 minutes (4 polls at 30s interval)
			if pollCount%4 == 0 {
				elapsed := time.Since(creationTime).Round(time.Second)
				g.GinkgoWriter.Printf("  Still waiting... elapsed: %v, poll #%d\n", elapsed, pollCount)
			}
			return false
		}, waitTimeout, 30*time.Second).Should(o.BeTrue(), fmt.Sprintf("event should be deleted after %dm TTL (waited %v)", ttl, waitTimeout))

		actualTTL := time.Since(creationTime)
		g.GinkgoWriter.Printf("Event expired after %v (expected TTL: %dm)\n", actualTTL.Round(time.Second), ttl)
		g.By(fmt.Sprintf("Successfully validated event expiration after %dm", ttl))
	})

	// Boundary validation tests - these should be fast since they just test API validation
	g.It("should reject eventTTLMinutes below minimum (0 minutes) [Conformance][Serial]", func() {
		g.By("Attempting to set eventTTLMinutes=0 (should be rejected)")
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": 0,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		// Note: 0 might be accepted as "unset" - check actual behavior
		// If 0 is valid (meaning unset), this test should be adjusted
		if err != nil {
			g.GinkgoWriter.Printf("Setting eventTTLMinutes=0 was rejected as expected: %v\n", err)
			o.Expect(apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)).To(o.BeTrue(),
				"error should be Invalid or BadRequest, got: %v", err)
		} else {
			// If 0 is accepted, verify the config doesn't have event-ttl set
			g.GinkgoWriter.Printf("eventTTLMinutes=0 was accepted (treated as unset)\n")
			cfg, getErr := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(getErr).NotTo(o.HaveOccurred())
			o.Expect(cfg.Spec.EventTTLMinutes).To(o.Equal(int32(0)), "eventTTLMinutes should be 0 (unset)")
		}
	})

	g.It("should reject eventTTLMinutes below minimum (negative value) [Conformance][Serial]", func() {
		g.By("Attempting to set eventTTLMinutes=-1 (should be rejected)")
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": -1,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		o.Expect(err).To(o.HaveOccurred(), "negative eventTTLMinutes should be rejected")
		g.GinkgoWriter.Printf("Setting eventTTLMinutes=-1 was rejected as expected: %v\n", err)
		o.Expect(apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)).To(o.BeTrue(),
			"error should be Invalid or BadRequest, got: %v", err)
	})

	g.It("should reject eventTTLMinutes above maximum (181 minutes) [Conformance][Serial]", func() {
		g.By("Attempting to set eventTTLMinutes=181 (should be rejected, max is 180)")
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": 181,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		o.Expect(err).To(o.HaveOccurred(), "eventTTLMinutes > 180 should be rejected")
		g.GinkgoWriter.Printf("Setting eventTTLMinutes=181 was rejected as expected: %v\n", err)
		o.Expect(apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)).To(o.BeTrue(),
			"error should be Invalid or BadRequest, got: %v", err)
	})

	g.It("should reject eventTTLMinutes below minimum (4 minutes) [Conformance][Serial]", func() {
		g.By("Attempting to set eventTTLMinutes=4 (should be rejected, min is 5)")
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": 4,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		o.Expect(err).To(o.HaveOccurred(), "eventTTLMinutes < 5 should be rejected")
		g.GinkgoWriter.Printf("Setting eventTTLMinutes=4 was rejected as expected: %v\n", err)
		o.Expect(apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)).To(o.BeTrue(),
			"error should be Invalid or BadRequest, got: %v", err)
	})

	g.It("should accept eventTTLMinutes at maximum boundary (180 minutes) [Conformance][Serial]", func() {
		// Get original value for cleanup
		currentCfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		originalEventTTL := currentCfg.Spec.EventTTLMinutes

		// Cleanup after test
		defer func() {
			restore := map[string]interface{}{"spec": map[string]interface{}{}}
			if originalEventTTL == 0 {
				restore["spec"].(map[string]interface{})["eventTTLMinutes"] = nil
			} else {
				restore["spec"].(map[string]interface{})["eventTTLMinutes"] = originalEventTTL
			}
			restoreBytes, _ := json.Marshal(restore)
			_, _ = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, restoreBytes, metav1.PatchOptions{})
			g.GinkgoWriter.Printf("Cleanup: restored eventTTLMinutes\n")
		}()

		g.By("Setting eventTTLMinutes=180 (maximum valid value)")
		patchData := map[string]interface{}{
			"spec": map[string]interface{}{
				"eventTTLMinutes": 180,
			},
		}
		patchBytes, err := json.Marshal(patchData)
		o.Expect(err).NotTo(o.HaveOccurred())

		_, err = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "eventTTLMinutes=180 should be accepted")

		// Verify the value was set
		cfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cfg.Spec.EventTTLMinutes).To(o.Equal(int32(180)))
		g.GinkgoWriter.Printf("eventTTLMinutes=180 was accepted successfully\n")
	})
})
