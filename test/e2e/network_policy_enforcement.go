package e2e

import (
	"context"
	"fmt"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][NetworkPolicy] should enforce NetworkPolicy allow/deny basics in a test namespace", func() {
		testGenericNetworkPolicyEnforcement()
	})
	g.It("[Operator][NetworkPolicy] should enforce kube-apiserver-operator NetworkPolicies", func() {
		testKubeAPIServerOperatorNetworkPolicyEnforcement()
	})
	g.It("[Operator][NetworkPolicy] should enforce cross-namespace ingress traffic", func() {
		testCrossNamespaceIngressEnforcement()
	})
	g.It("[Operator][NetworkPolicy] should allow metrics but block other ports", func() {
		testMetricsOpenButOtherPortsBlocked()
	})
	g.It("[Operator][NetworkPolicy] should allow metrics ingress from any namespace", func() {
		testMetricsIngressOpenAccess()
	})
})

func testGenericNetworkPolicyEnforcement() {
	ctx := context.Background()
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Creating a temporary namespace for policy enforcement checks")
	nsName := createTestNamespace(kubeClient.CoreV1().Namespaces(), "np-enforcement-")
	g.DeferCleanup(func() {
		g.GinkgoWriter.Printf("deleting test namespace %s\n", nsName)
		_ = kubeClient.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
	})

	serverName := "np-server"
	clientLabels := map[string]string{"app": "np-client"}
	serverLabels := map[string]string{"app": "np-server"}

	g.GinkgoWriter.Printf("creating netexec server pod %s/%s\n", nsName, serverName)
	serverPod := netexecPod(serverName, nsName, serverLabels, 8080)
	_, err = kubeClient.CoreV1().Pods(nsName).Create(ctx, serverPod, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(waitForPodReady(ctx, kubeClient, nsName, serverName)).NotTo(o.HaveOccurred())

	server, err := kubeClient.CoreV1().Pods(nsName).Get(ctx, serverName, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(server.Status.PodIPs).NotTo(o.BeEmpty())
	serverIPs := podIPs(server)
	g.GinkgoWriter.Printf("server pod %s/%s ips=%v\n", nsName, serverName, serverIPs)

	g.By("Verifying allow-all when no policies select the pod")
	expectConnectivity(ctx, kubeClient, nsName, clientLabels, serverIPs, 8080, true)

	g.By("Applying default deny and verifying traffic is blocked")
	g.GinkgoWriter.Printf("creating default-deny policy in %s\n", nsName)
	_, err = kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx, defaultDenyPolicy("default-deny", nsName), metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Adding ingress allow only and verifying traffic is still blocked")
	g.GinkgoWriter.Printf("creating allow-ingress policy in %s\n", nsName)
	_, err = kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx, allowIngressPolicy("allow-ingress", nsName, serverLabels, clientLabels, 8080), metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	expectConnectivity(ctx, kubeClient, nsName, clientLabels, serverIPs, 8080, false)

	g.By("Adding egress allow and verifying traffic is permitted")
	g.GinkgoWriter.Printf("creating allow-egress policy in %s\n", nsName)
	_, err = kubeClient.NetworkingV1().NetworkPolicies(nsName).Create(ctx, allowEgressPolicy("allow-egress", nsName, clientLabels, serverLabels, 8080), metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	expectConnectivity(ctx, kubeClient, nsName, clientLabels, serverIPs, 8080, true)
}

func testKubeAPIServerOperatorNetworkPolicyEnforcement() {
	ctx := context.Background()
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	namespace := "openshift-kube-apiserver-operator"
	serverLabels := map[string]string{"app": "kube-apiserver-operator"}
	clientLabels := map[string]string{"app": "kube-apiserver-operator"}
	policy, err := kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(ctx, operatorAllowPolicyName, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Creating kube-apiserver-operator test pod for policy checks")
	g.GinkgoWriter.Printf("creating kube-apiserver-operator server pod in %s\n", namespace)
	serverIPs, cleanupServer := createServerPod(ctx, kubeClient, namespace, fmt.Sprintf("np-kas-op-server-%s", rand.String(5)), serverLabels, 8443)
	g.DeferCleanup(cleanupServer)

	allowedFromSameNamespace := ingressAllowsFromNamespace(policy, namespace, clientLabels, 8443)
	g.By("Verifying within-namespace traffic matches policy")
	expectConnectivity(ctx, kubeClient, namespace, clientLabels, serverIPs, 8443, allowedFromSameNamespace)

	g.By("Verifying cross-namespace traffic from monitoring is allowed")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, serverIPs, 8443, true)

	g.By("Verifying unauthorized ports are denied")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, serverIPs, 12345, false)
}

func testCrossNamespaceIngressEnforcement() {
	ctx := context.Background()
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Creating test server pods in kube-apiserver-operator namespace")
	kasOperatorIPs, cleanupKASOperator := createServerPod(ctx, kubeClient, "openshift-kube-apiserver-operator", fmt.Sprintf("np-kas-op-xns-%s", rand.String(5)), map[string]string{"app": "kube-apiserver-operator"}, 8443)
	g.DeferCleanup(cleanupKASOperator)

	g.By("Testing cross-namespace ingress: monitoring -> kube-apiserver-operator:8443")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, kasOperatorIPs, 8443, true)

	g.By("Testing cross-namespace ingress: any pod from monitoring can access metrics")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app": "any-label"}, kasOperatorIPs, 8443, true)
}

func testMetricsOpenButOtherPortsBlocked() {
	ctx := context.Background()
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Creating test server pod in kube-apiserver-operator namespace")
	kasOperatorIPs, cleanupKASOperator := createServerPod(ctx, kubeClient, "openshift-kube-apiserver-operator", fmt.Sprintf("np-kas-op-unauth-%s", rand.String(5)), map[string]string{"app": "kube-apiserver-operator"}, 8443)
	g.DeferCleanup(cleanupKASOperator)

	g.By("Testing metrics port 8443 is now open: default namespace -> kube-apiserver-operator:8443")
	expectConnectivity(ctx, kubeClient, "default", map[string]string{"test": "client"}, kasOperatorIPs, 8443, true)

	g.By("Testing metrics port 8443 from openshift-etcd with custom app label: should be denied")
	expectConnectivity(ctx, kubeClient, "openshift-etcd", map[string]string{"test": "client"}, kasOperatorIPs, 8443, false)

	g.By("Testing port-based blocking: unauthorized ports are still blocked")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, kasOperatorIPs, 9999, false)

	g.By("Testing multiple unauthorized ports are still blocked by default-deny")
	for _, port := range []int32{80, 443, 8080, 22, 3306, 9090} {
		expectConnectivity(ctx, kubeClient, "default", map[string]string{"test": "any-pod"}, kasOperatorIPs, port, false)
	}
}

func testMetricsIngressOpenAccess() {
	ctx := context.Background()
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Creating test server pod in kube-apiserver-operator namespace with operator labels")
	kasOperatorIPs, cleanupKASOperator := createServerPod(ctx, kubeClient, "openshift-kube-apiserver-operator", fmt.Sprintf("np-metrics-test-%s", rand.String(5)), map[string]string{"app": "kube-apiserver-operator"}, 8443)
	g.DeferCleanup(cleanupKASOperator)

	g.By("Testing allow-to-metrics policy: monitoring namespace can access metrics -> operator:8443")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, kasOperatorIPs, 8443, true)

	g.By("Testing metrics policy: etcd namespace with custom app label should be denied")
	expectConnectivity(ctx, kubeClient, "openshift-etcd", map[string]string{"test": "metrics-client"}, kasOperatorIPs, 8443, false)

	g.By("Testing metrics policy: console namespace with custom app label can access metrics")
	expectConnectivity(ctx, kubeClient, "openshift-console", map[string]string{"custom-app": "test-client"}, kasOperatorIPs, 8443, true)

	g.By("Testing allow-to-metrics policy: default namespace can access metrics -> operator:8443")
	expectConnectivity(ctx, kubeClient, "default", map[string]string{"test": "client"}, kasOperatorIPs, 8443, true)

	g.By("Testing allow-to-metrics policy: same namespace can access metrics -> operator:8443")
	expectConnectivity(ctx, kubeClient, "openshift-kube-apiserver-operator", map[string]string{"app": "kube-apiserver-operator"}, kasOperatorIPs, 8443, true)

	g.By("Testing default-deny still blocks unauthorized ports")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, kasOperatorIPs, 9090, false)
}
