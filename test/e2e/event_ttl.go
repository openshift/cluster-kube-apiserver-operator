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
		timeout          = 20 * time.Minute // Increased for feature gate changes which trigger full cluster rollouts
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
				&ginkgoLogger{},
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
			&ginkgoLogger{},
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

		// Debug: print relevant part of config
		for _, line := range strings.Split(configData, "\n") {
			if strings.Contains(line, "event-ttl") || strings.Contains(line, "eventTTL") {
				g.GinkgoWriter.Printf("  Config line: %s\n", strings.TrimSpace(line))
			}
		}

		// Debug: Check kube-apiserver pod args to verify --event-ttl is set
		g.GinkgoWriter.Printf("Checking kube-apiserver pods for --event-ttl flag...\n")
		pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "apiserver=true",
		})
		o.Expect(err).NotTo(o.HaveOccurred())
		for _, pod := range pods.Items {
			for _, container := range pod.Spec.Containers {
				if container.Name == "kube-apiserver" {
					for _, arg := range container.Args {
						if strings.Contains(arg, "event-ttl") {
							g.GinkgoWriter.Printf("  Pod %s: %s\n", pod.Name, arg)
						}
					}
				}
			}
		}

		g.By(fmt.Sprintf("Successfully verified eventTTLMinutes=%d configuration", ttl))

		g.By(fmt.Sprintf("Validating that events actually expire after %d minutes", ttl))
		testNamespace := fmt.Sprintf("event-ttl-test-%d", time.Now().Unix())
		_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		g.GinkgoWriter.Printf("Created test namespace: %s\n", testNamespace)

		defer func() {
			_ = kubeClient.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
			g.GinkgoWriter.Printf("Deleted test namespace: %s\n", testNamespace)
		}()

		// Create a test event
		eventName := fmt.Sprintf("ttl-test-event-%d", time.Now().Unix())
		testEvent := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      eventName,
				Namespace: testNamespace,
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Pod",
				Namespace: testNamespace,
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

		createdEvent, err := kubeClient.CoreV1().Events(testNamespace).Create(ctx, testEvent, metav1.CreateOptions{})
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
			event, err := kubeClient.CoreV1().Events(testNamespace).Get(ctx, eventName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					g.GinkgoWriter.Printf("  Event deleted! (poll #%d)\n", pollCount)
					return true
				}
				// Print unexpected errors
				g.GinkgoWriter.Printf("  Unexpected error getting event: %v\n", err)
				return false
			}
			// Log progress every 2 minutes (4 polls at 30s interval) with event details
			if pollCount%4 == 0 {
				elapsed := time.Since(creationTime).Round(time.Second)
				g.GinkgoWriter.Printf("  Still waiting... elapsed: %v, poll #%d\n", elapsed, pollCount)
				g.GinkgoWriter.Printf("    Event details: FirstTimestamp=%v, LastTimestamp=%v, Count=%d\n",
					event.FirstTimestamp.Time.Format(time.RFC3339),
					event.LastTimestamp.Time.Format(time.RFC3339),
					event.Count)
				g.GinkgoWriter.Printf("    Event age: %v\n", time.Since(event.LastTimestamp.Time).Round(time.Second))

				// List all events in namespace to see if GC is working at all
				events, listErr := kubeClient.CoreV1().Events(testNamespace).List(ctx, metav1.ListOptions{})
				if listErr == nil {
					g.GinkgoWriter.Printf("    Total events in namespace: %d\n", len(events.Items))
				}

				// Check kube-controller-manager status for event GC
				kcmPods, kcmErr := kubeClient.CoreV1().Pods("openshift-kube-controller-manager").List(ctx, metav1.ListOptions{
					LabelSelector: "app=kube-controller-manager",
				})
				if kcmErr == nil {
					for _, kcmPod := range kcmPods.Items {
						ready := "NotReady"
						for _, cond := range kcmPod.Status.Conditions {
							if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
								ready = "Ready"
							}
						}
						g.GinkgoWriter.Printf("    KCM pod %s: Phase=%s, %s\n", kcmPod.Name, kcmPod.Status.Phase, ready)
					}
				}
			}
			return false
		}, waitTimeout, 30*time.Second).Should(o.BeTrue(), fmt.Sprintf("event should be deleted after %dm TTL (waited %v)", ttl, waitTimeout))

		actualTTL := time.Since(creationTime)
		g.GinkgoWriter.Printf("Event expired after %v (expected TTL: %dm)\n", actualTTL.Round(time.Second), ttl)
		g.By(fmt.Sprintf("Successfully validated event expiration after %dm", ttl))
	})
})

// ginkgoLogger adapts Ginkgo's logging to library.LoggingT interface
type ginkgoLogger struct{}

func (l *ginkgoLogger) Logf(format string, args ...interface{}) {
	g.GinkgoWriter.Printf(format+"\n", args...)
}
