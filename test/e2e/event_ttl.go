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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][FeatureGate:EventTTL][Skipped:HyperShift][Skipped:MicroShift] Event TTL Configuration", g.Ordered, func() {
	var (
		kubeClient     *kubernetes.Clientset
		configClient   *configclient.Clientset
		operatorClient *operatorclientset.Clientset
		ctx            context.Context
	)

	const (
		successThreshold = 3
		successInterval  = 1 * time.Minute
		pollInterval     = 30 * time.Second
		timeout          = 15 * time.Minute
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

		// Enable feature gate if not already enabled
		if !eventTTLEnabled {
			g.By("Enabling EventTTL feature gate")
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

			_, err = configClient.ConfigV1().FeatureGates().Patch(ctx, "cluster", types.MergePatchType, patchBytes, metav1.PatchOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			g.GinkgoWriter.Printf("Feature gate patch applied successfully\n")

			// Wait for feature gate to be enabled
			g.By("Waiting for EventTTL feature gate to be enabled")
			o.Eventually(func() bool {
				fg, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
				if err != nil {
					return false
				}
				for _, fgDetails := range fg.Status.FeatureGates {
					for _, enabled := range fgDetails.Enabled {
						if string(enabled.Name) == "EventTTL" {
							return true
						}
					}
				}
				return false
			}, 10*time.Minute, 10*time.Second).Should(o.BeTrue(), "EventTTL feature gate should be enabled")
			g.GinkgoWriter.Printf("EventTTL feature gate is now enabled\n")

			// Wait for API server to stabilize after feature gate change
			g.By("Waiting for API server to stabilize after feature gate change")
			err = libgotest.WaitForPodsToStabilizeOnTheSameRevision(
				&ginkgoLogger{},
				kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
				"apiserver=true",
				successThreshold, successInterval, pollInterval, timeout,
			)
			o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize after feature gate change")
			g.GinkgoWriter.Printf("API server stabilized after feature gate change\n")
		}
	})

	g.It("should configure eventTTLMinutes and verify it in kube-apiserver config [Conformance][Serial][Timeout:30m]", func() {
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
			_, _ = operatorClient.OperatorV1().KubeAPIServers().Patch(ctx, "cluster", types.MergePatchType, restoreBytes, metav1.PatchOptions{})
			g.GinkgoWriter.Printf("Cleanup: restored eventTTLMinutes to original value\n")
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

		o.Eventually(func() bool {
			configMap, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "config", metav1.GetOptions{})
			if err != nil {
				return false
			}
			configData, found := configMap.Data["config.yaml"]
			if !found {
				return false
			}
			return strings.Contains(configData, "event-ttl") && strings.Contains(configData, expectedTTL)
		}, 2*time.Minute, 5*time.Second).Should(o.BeTrue(), fmt.Sprintf("event-ttl=%s should be in config", expectedTTL))

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

		// Poll for event deletion (TTL + 3 minutes buffer for GC)
		waitTimeout := time.Duration(ttl+3) * time.Minute
		g.GinkgoWriter.Printf("Waiting up to %v for event to expire...\n", waitTimeout)

		o.Eventually(func() bool {
			_, err := kubeClient.CoreV1().Events(testNamespace).Get(ctx, eventName, metav1.GetOptions{})
			if err != nil && strings.Contains(err.Error(), "not found") {
				return true
			}
			return false
		}, waitTimeout, 30*time.Second).Should(o.BeTrue(), fmt.Sprintf("event should be deleted after %dm TTL", ttl))

		actualTTL := time.Since(creationTime)
		g.GinkgoWriter.Printf("Event expired after %v (expected TTL: %dm)\n", actualTTL.Round(time.Minute), ttl)
		g.By(fmt.Sprintf("Successfully validated event expiration after %dm", ttl))
	})
})

// ginkgoLogger adapts Ginkgo's logging to library.LoggingT interface
type ginkgoLogger struct{}

func (l *ginkgoLogger) Logf(format string, args ...interface{}) {
	g.GinkgoWriter.Printf(format+"\n", args...)
}
