package e2e

import (
	"context"
	"fmt"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	libgotest "github.com/openshift/library-go/test/library"
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][OCPFeatureGate:EventTTL][Skipped:HyperShift][Skipped:MicroShift] Event TTL Configuration", func() {
	var (
		kubeClient     *kubernetes.Clientset
		operatorClient *operatorclientset.Clientset
		ctx            context.Context
	)

	g.BeforeEach(func() {
		ctx = context.TODO()
		kubeConfig, err := libgotest.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred())

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	g.It("should configure eventTTLMinutes and verify events expire [Conformance][Serial][Timeout:60m][Late]", func() {
		cfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		originalEventTTL := cfg.Spec.EventTTLMinutes

		defer func() {
			g.By("Cleaning up eventTTLMinutes configuration")
			cfg, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())

			cfg.Spec.EventTTLMinutes = originalEventTTL
			_, err = operatorClient.OperatorV1().KubeAPIServers().Update(ctx, cfg, metav1.UpdateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())

			g.By("Waiting for API server to stabilize after cleanup")
			err = testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
				g.GinkgoT(),
				kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
			)
			o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize after cleanup")
		}()

		testEventTTLMinutes := int32(5)

		g.By(fmt.Sprintf("Configuring eventTTLMinutes=%d", testEventTTLMinutes))
		cfg, err = operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		cfg.Spec.EventTTLMinutes = testEventTTLMinutes
		updatedCfg, err := operatorClient.OperatorV1().KubeAPIServers().Update(ctx, cfg, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(updatedCfg.Spec.EventTTLMinutes).To(o.Equal(testEventTTLMinutes))

		g.By("Waiting for API server to stabilize with new eventTTLMinutes")
		err = testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
		)
		o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize after eventTTLMinutes change")

		g.By("Creating a test event to verify TTL behavior")
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
			},
			Reason:        "EventTTLTest",
			Message:       fmt.Sprintf("Test event - should expire after %dm", testEventTTLMinutes),
			Type:          corev1.EventTypeNormal,
			LastTimestamp: metav1.Now(),
		}

		_, err = kubeClient.CoreV1().Events(operatorclient.TargetNamespace).Create(ctx, testEvent, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By(fmt.Sprintf("Waiting for event to expire after %dm TTL", testEventTTLMinutes))
		// Add 10 minutes buffer for flake prevention: etcd doesn't delete expired keys instantly,
		// it processes them in batches during compaction windows. System load and leader elections
		// can also delay the background process that reaps expired leases.
		waitTimeout := time.Duration(testEventTTLMinutes+10) * time.Minute
		o.Eventually(func() error {
			_, err := kubeClient.CoreV1().Events(operatorclient.TargetNamespace).Get(ctx, eventName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("event still exists")
		}, waitTimeout, 30*time.Second).Should(o.Succeed(), "event should be deleted after TTL")
	})
})
