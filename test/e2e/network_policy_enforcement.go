package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator [Operator][NetworkPolicy] enforcement", func() {
	var (
		ctx            context.Context
		kubeClient     kubernetes.Interface
		operatorPodIPs []string
	)

	g.BeforeEach(func() {
		ctx = context.Background()
		config := getClientConfigForTest()
		var err error
		kubeClient, err = kubernetes.NewForConfig(config)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Getting operator pod IPs")
		pods, err := kubeClient.CoreV1().Pods(kasOperatorNamespace).List(ctx,
			metav1.ListOptions{LabelSelector: "app=kube-apiserver-operator"})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pods.Items).NotTo(o.BeEmpty(), "expected at least one operator pod")
		operatorPodIPs = podIPs(&pods.Items[0])
	})

	// withServiceAccount ensures a test service account exists in the given namespace
	// and registers cleanup to remove it after the test.
	withServiceAccount := func(namespace string) {
		g.GinkgoHelper()
		ensureTestServiceAccount(ctx, kubeClient, namespace)
		g.DeferCleanup(func() {
			deleteServiceAccount(ctx, kubeClient, namespace, "netpolicy-test-sa")
		})
	}

	g.Context("cross-namespace connectivity to operator metrics port", func() {
		type connectivityCase struct {
			sourceNS string
			labels   map[string]string
			port     int32
			allowed  bool
		}

		g.DescribeTable("",
			func(tc connectivityCase) {
				withServiceAccount(tc.sourceNS)
				expectConnectivity(ctx, kubeClient, tc.sourceNS, tc.labels,
					operatorPodIPs, tc.port, tc.allowed)
			},

			// The operator's ingress policy allows all sources on port 8443.
			// Whether traffic succeeds depends on the source namespace's egress policies.
			g.Entry("monitoring → :8443 (allowed: monitoring permits prometheus egress)",
				connectivityCase{
					sourceNS: "openshift-monitoring",
					labels:   map[string]string{"app.kubernetes.io/name": "prometheus"},
					port:     8443,
					allowed:  true,
				}),
			g.Entry("default → :8443 (allowed: no egress restrictions in default namespace)",
				connectivityCase{
					sourceNS: "default",
					labels:   map[string]string{"test": "client"},
					port:     8443,
					allowed:  true,
				}),
			g.Entry("console → :8443 (allowed: console permits egress)",
				connectivityCase{
					sourceNS: "openshift-console",
					labels:   map[string]string{"custom-app": "test-client"},
					port:     8443,
					allowed:  true,
				}),

			// etcd has a default-deny egress policy; only guard/installer/pruner/backup/tnf pods
			// are in the egress allowlist. Test pods with arbitrary labels are blocked.
			g.Entry("etcd → :8443 (blocked: etcd default-deny egress, test pod not in allowlist)",
				connectivityCase{
					sourceNS: "openshift-etcd",
					labels:   map[string]string{"test": "metrics-client"},
					port:     8443,
					allowed:  false,
				}),

			// Non-metrics ports are blocked by the operator's default-deny ingress policy.
			g.Entry("monitoring → :9090 (blocked: operator ingress only allows 8443)",
				connectivityCase{
					sourceNS: "openshift-monitoring",
					labels:   map[string]string{"app.kubernetes.io/name": "prometheus"},
					port:     9090,
					allowed:  false,
				}),
		)
	})

	g.Context("basic NetworkPolicy enforcement in a test namespace", func() {
		var nsName string

		g.BeforeEach(func() {
			nsName = createTestNamespace(kubeClient.CoreV1().Namespaces(), "np-test-")
			g.DeferCleanup(func() {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
			})
			ensureTestServiceAccount(ctx, kubeClient, nsName)
		})

		g.It("should allow all traffic when no policies exist", func() {
			serverIPs, cleanup := createServerPod(ctx, kubeClient, nsName,
				"server", map[string]string{"app": "server"}, 8080)
			g.DeferCleanup(cleanup)

			expectConnectivity(ctx, kubeClient, nsName,
				map[string]string{"app": "client"}, serverIPs, 8080, true)
		})

		g.It("should block traffic after applying default-deny", func() {
			serverIPs, cleanup := createServerPod(ctx, kubeClient, nsName,
				"server", map[string]string{"app": "server"}, 8080)
			g.DeferCleanup(cleanup)

			g.By("Applying default-deny policy")
			_, err := kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx,
				defaultDenyPolicy("default-deny", nsName), metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())

			expectConnectivity(ctx, kubeClient, nsName,
				map[string]string{"app": "client"}, serverIPs, 8080, false)
		})

		g.It("should allow traffic when both ingress and egress rules match", func() {
			serverLabels := map[string]string{"app": "server"}
			clientLabels := map[string]string{"app": "client"}

			serverIPs, cleanup := createServerPod(ctx, kubeClient, nsName,
				"server", serverLabels, 8080)
			g.DeferCleanup(cleanup)

			g.By("Applying default-deny, then ingress+egress allow rules")
			_, err := kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx,
				defaultDenyPolicy("default-deny", nsName), metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			_, err = kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx,
				allowIngressPolicy("allow-in", nsName, serverLabels, clientLabels, 8080),
				metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			_, err = kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx,
				allowEgressPolicy("allow-out", nsName, clientLabels, serverLabels, 8080),
				metav1.CreateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())

			expectConnectivity(ctx, kubeClient, nsName,
				clientLabels, serverIPs, 8080, true)
		})
	})
})
