package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"
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
	g.It("[Serial][Operator][Feature:NetworkPolicy] should ensure kube-apiserver NetworkPolicies are defined", func() {
		testKubeAPIServerNetworkPolicies()
	})
	g.It("[Serial][Operator][Feature:NetworkPolicy] should restore kube-apiserver NetworkPolicies after delete or mutation", func() {
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
	err = test.WaitForClusterOperatorAvailableNotProgressingNotDegraded(configClient, "kube-apiserver")
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Validating NetworkPolicies in openshift-kube-apiserver-operator")
	operatorDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName)
	logNetworkPolicySummary("kube-apiserver-operator/default-deny-all", operatorDefaultDeny)
	logNetworkPolicyDetails("kube-apiserver-operator/default-deny-all", operatorDefaultDeny)
	o.Expect(operatorDefaultDeny).To(BeDefaultDenyPolicy())

	operatorAllowPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, operatorAllowPolicyName)
	logNetworkPolicySummary("kube-apiserver-operator/"+operatorAllowPolicyName, operatorAllowPolicy)
	logNetworkPolicyDetails("kube-apiserver-operator/"+operatorAllowPolicyName, operatorAllowPolicy)
	o.Expect(operatorAllowPolicy).To(SelectPods("app", "kube-apiserver-operator"))
	o.Expect(operatorAllowPolicy).To(AllowIngressOnPort(8443))
	o.Expect(operatorAllowPolicy).To(AllowAllTCPEgress())

	g.By("Validating NetworkPolicies in openshift-kube-apiserver")
	operandDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, defaultDenyAllPolicyName)
	logNetworkPolicySummary("kube-apiserver/default-deny-all", operandDefaultDeny)
	logNetworkPolicyDetails("kube-apiserver/default-deny-all", operandDefaultDeny)
	o.Expect(operandDefaultDeny).To(BeDefaultDenyPolicy())

	operandAllowPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, operandAllowPolicyName)
	logNetworkPolicySummary("kube-apiserver/"+operandAllowPolicyName, operandAllowPolicy)
	logNetworkPolicyDetails("kube-apiserver/"+operandAllowPolicyName, operandAllowPolicy)
	o.Expect(operandAllowPolicy).To(SelectPodsExpression("app", []string{"guard", "installer", "pruner", "openshift-kms-preflight"}))
	o.Expect(operandAllowPolicy).To(AllowAllTCPEgress())

	g.By("Verifying pods are ready in kube-apiserver namespaces")
	waitForPodsReadyByLabel(ctx, kubeClient, kubeAPIServerOperatorNamespace, "app=kube-apiserver-operator")
}

func testKubeAPIServerNetworkPolicyReconcile() {
	ctx := context.Background()
	g.By("Creating Kubernetes clients")
	kubeConfig := getClientConfigForTest()
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	g.By("Capturing expected NetworkPolicy specs across all namespaces")
	expectedOperatorPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, operatorAllowPolicyName)
	expectedOperandPolicy := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, operandAllowPolicyName)
	expectedOperatorDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName)
	expectedOperandDefaultDeny := getNetworkPolicy(ctx, kubeClient, kubeAPIServerNamespace, defaultDenyAllPolicyName)

	policiesToDelete := []*networkingv1.NetworkPolicy{
		expectedOperatorPolicy,
		expectedOperandPolicy,
		expectedOperatorDefaultDeny,
		expectedOperandDefaultDeny,
	}

	g.By("Deleting all NetworkPolicies simultaneously and waiting for restoration")
	deleteAndWaitForAllRestored(ctx, kubeClient, policiesToDelete)

	g.By("Mutating all NetworkPolicies simultaneously and waiting for reconciliation")
	policiesToMutate := []struct{ namespace, name string }{
		{kubeAPIServerOperatorNamespace, operatorAllowPolicyName},
		{kubeAPIServerNamespace, operandAllowPolicyName},
		{kubeAPIServerOperatorNamespace, defaultDenyAllPolicyName},
		{kubeAPIServerNamespace, defaultDenyAllPolicyName},
	}
	mutateAndWaitForAllReconciled(ctx, kubeClient, policiesToMutate)

	g.By("Checking NetworkPolicy-related events (best-effort)")
	logNetworkPolicyEvents(ctx, kubeClient, []string{kubeAPIServerOperatorNamespace, kubeAPIServerNamespace}, operatorAllowPolicyName)
}
