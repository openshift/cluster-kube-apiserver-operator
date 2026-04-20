package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

const (
	kubeAPIServerNamespace         = "openshift-kube-apiserver"
	kubeAPIServerOperatorNamespace = "openshift-kube-apiserver-operator"
	defaultDenyAllPolicyName       = "default-deny"
	operatorAllowPolicyName        = "allow-all-egress-and-metrics-ingress"
	operandAllowPolicyName         = "allow-all-egress"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[NetworkPolicy] should ensure kube-apiserver NetworkPolicies are defined", func() {
		testKubeAPIServerNetworkPolicies()
	})
	g.It("[NetworkPolicy][Disruptive][Serial] should restore kube-apiserver NetworkPolicies after delete or mutation[Timeout:30m]", func() {
		testKubeAPIServerNetworkPolicyReconcile()
	})
})

func testKubeAPIServerNetworkPolicies() {
	ctx := context.Background()
	g.By("Creating Kubernetes clients")
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())
	configClient, err := configclient.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Waiting for kube-apiserver ClusterOperator to be stable")
	err = test.WaitForClusterOperatorAvailableNotProgressingNotDegraded(g.GinkgoTB(), configClient, "kube-apiserver")
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Validating NetworkPolicies in openshift-kube-apiserver-operator")
	operatorDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName)
	logNetworkPolicySummary("kube-apiserver-operator/default-deny-all", operatorDefaultDeny)
	logNetworkPolicyDetails("kube-apiserver-operator/default-deny-all", operatorDefaultDeny)
	requireDefaultDenyAll(operatorDefaultDeny)

	operatorAllowPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, operatorAllowPolicyName)
	logNetworkPolicySummary("kube-apiserver-operator/"+operatorAllowPolicyName, operatorAllowPolicy)
	logNetworkPolicyDetails("kube-apiserver-operator/"+operatorAllowPolicyName, operatorAllowPolicy)
	requirePodSelectorLabel(operatorAllowPolicy, "app", "kube-apiserver-operator")
	requireIngressPort(operatorAllowPolicy, corev1.ProtocolTCP, 8443)
	requireIngressAllowAll(operatorAllowPolicy, 8443)
	logEgressAllowAllTCP(operatorAllowPolicy)

	g.By("Validating NetworkPolicies in openshift-kube-apiserver")
	operandDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, defaultDenyAllPolicyName)
	logNetworkPolicySummary("kube-apiserver/default-deny-all", operandDefaultDeny)
	logNetworkPolicyDetails("kube-apiserver/default-deny-all", operandDefaultDeny)
	requireDefaultDenyAll(operandDefaultDeny)

	operandAllowPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, operandAllowPolicyName)
	logNetworkPolicySummary("kube-apiserver/"+operandAllowPolicyName, operandAllowPolicy)
	logNetworkPolicyDetails("kube-apiserver/"+operandAllowPolicyName, operandAllowPolicy)
	// Verify it selects guard, installer, and pruner pods with In expression
	requirePodSelectorExpression(operandAllowPolicy, "app", []string{"guard", "installer", "pruner"})
	logEgressAllowAllTCP(operandAllowPolicy)

	g.By("Verifying pods are ready in kube-apiserver namespaces")
	waitForPodsReadyByLabel(ctx, kubeClient, kubeAPIServerOperatorNamespace, "app=kube-apiserver-operator")
}

func testKubeAPIServerNetworkPolicyReconcile() {
	ctx := context.Background()
	g.By("Creating Kubernetes clients")
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())
	configClient, err := configclient.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Waiting for kube-apiserver ClusterOperator to be stable")
	err = test.WaitForClusterOperatorAvailableNotProgressingNotDegraded(g.GinkgoTB(), configClient, "kube-apiserver")
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Capturing expected NetworkPolicy specs")
	expectedOperatorPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, operatorAllowPolicyName)
	expectedOperandPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, operandAllowPolicyName)
	expectedOperatorDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName)
	expectedOperandDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, defaultDenyAllPolicyName)

	g.By("Deleting main policies and waiting for restoration")
	restoreNetworkPolicy(ctx, kubeClient, expectedOperatorPolicy)
	restoreNetworkPolicy(ctx, kubeClient, expectedOperandPolicy)

	g.By("Deleting default-deny-all policies and waiting for restoration")
	restoreNetworkPolicy(ctx, kubeClient, expectedOperatorDefaultDeny)
	restoreNetworkPolicy(ctx, kubeClient, expectedOperandDefaultDeny)

	g.By("Mutating main policies and waiting for reconciliation")
	mutateAndRestoreNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, operatorAllowPolicyName)
	mutateAndRestoreNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, operandAllowPolicyName)

	g.By("Mutating default-deny-all policies and waiting for reconciliation")
	mutateAndRestoreNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName)
	mutateAndRestoreNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, defaultDenyAllPolicyName)

	g.By("Checking NetworkPolicy-related events (best-effort)")
	logNetworkPolicyEvents(ctx, kubeClient, []string{kubeAPIServerOperatorNamespace, kubeAPIServerNamespace}, operatorAllowPolicyName)
}
