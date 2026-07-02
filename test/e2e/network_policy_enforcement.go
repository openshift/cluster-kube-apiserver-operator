package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Serial][Operator][Feature:NetworkPolicy] should enforce NetworkPolicy allow/deny basics in a test namespace", func() {
		testGenericNetworkPolicyEnforcement()
	})
	g.It("[Serial][Operator][Feature:NetworkPolicy] should enforce kube-apiserver-operator NetworkPolicies for cross-namespace traffic", func() {
		testKubeAPIServerOperatorNetworkPolicyEnforcement()
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

	g.By("Creating test service account")
	_, _ = ensureTestServiceAccount(ctx, kubeClient, nsName)
	// No need to defer cleanup - the entire namespace will be deleted

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

	g.By("Creating test service accounts in required namespaces")
	_, cleanupMonitoring := ensureTestServiceAccount(ctx, kubeClient, "openshift-monitoring")
	g.DeferCleanup(cleanupMonitoring)
	_, cleanupConsole := ensureTestServiceAccount(ctx, kubeClient, "openshift-console")
	g.DeferCleanup(cleanupConsole)
	_, cleanupDefault := ensureTestServiceAccount(ctx, kubeClient, "default")
	g.DeferCleanup(cleanupDefault)

	g.By("Getting IP of real kube-apiserver-operator pod")
	pods, err := kubeClient.CoreV1().Pods("openshift-kube-apiserver-operator").List(ctx, metav1.ListOptions{
		LabelSelector: "app=kube-apiserver-operator",
	})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(pods.Items).NotTo(o.BeEmpty(), "should have at least one operator pod")
	kasOperatorIPs := podIPs(&pods.Items[0])

	g.By("Verifying monitoring namespace with prometheus label can access operator metrics")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app.kubernetes.io/name": "prometheus"}, kasOperatorIPs, 8443, true)

	g.By("Verifying monitoring namespace with any label can access operator metrics")
	expectConnectivity(ctx, kubeClient, "openshift-monitoring", map[string]string{"app": "any-label"}, kasOperatorIPs, 8443, true)

	g.By("Verifying console namespace can access operator metrics")
	expectConnectivity(ctx, kubeClient, "openshift-console", map[string]string{"custom-app": "test-client"}, kasOperatorIPs, 8443, true)

	g.By("Verifying default namespace can access operator metrics")
	expectConnectivity(ctx, kubeClient, "default", map[string]string{"test": "client"}, kasOperatorIPs, 8443, true)
}
