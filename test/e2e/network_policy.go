package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

const (
	kasNamespace         = "openshift-kube-apiserver"
	kasOperatorNamespace = "openshift-kube-apiserver-operator"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator [Operator][NetworkPolicy]", func() {
	var (
		ctx          context.Context
		kubeClient   kubernetes.Interface
		configClient configclient.ConfigV1Interface
	)

	g.BeforeEach(func() {
		ctx = context.Background()
		config := getClientConfigForTest()
		var err error
		kubeClient, err = kubernetes.NewForConfig(config)
		o.Expect(err).NotTo(o.HaveOccurred())
		configClient, err = configclient.NewForConfig(config)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Waiting for kube-apiserver ClusterOperator to be stable")
		err = test.WaitForClusterOperatorAvailableNotProgressingNotDegraded(
			g.GinkgoTB(), configClient, "kube-apiserver")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	getPolicy := func(ns, name string) *networkingv1.NetworkPolicy {
		g.GinkgoHelper()
		p, err := kubeClient.NetworkingV1().NetworkPolicies(ns).Get(ctx, name, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "get NetworkPolicy %s/%s", ns, name)
		return p
	}

	g.Context("conformance", func() {
		g.Context("in the operator namespace", func() {
			g.It("should have a default-deny-all policy", func() {
				o.Expect(getPolicy(kasOperatorNamespace, "default-deny")).To(BeDefaultDenyPolicy())
			})

			g.It("should allow egress and metrics ingress for the operator pod", func() {
				policy := getPolicy(kasOperatorNamespace, "allow-all-egress-and-metrics-ingress")
				o.Expect(policy).To(SelectPods("app", "kube-apiserver-operator"))
				o.Expect(policy).To(AllowIngressOnPort(8443))
				o.Expect(policy).To(AllowAllTCPEgress())
			})

			g.It("should have ready operator pods", func() {
				pods, err := kubeClient.CoreV1().Pods(kasOperatorNamespace).List(ctx,
					metav1.ListOptions{LabelSelector: "app=kube-apiserver-operator"})
				o.Expect(err).NotTo(o.HaveOccurred())
				o.Expect(pods.Items).NotTo(o.BeEmpty())
				for _, pod := range pods.Items {
					o.Expect(waitForPodReady(ctx, kubeClient, kasOperatorNamespace, pod.Name)).
						NotTo(o.HaveOccurred())
				}
			})
		})

		g.Context("in the operand namespace", func() {
			g.It("should have a default-deny-all policy", func() {
				o.Expect(getPolicy(kasNamespace, "default-deny")).To(BeDefaultDenyPolicy())
			})

			g.It("should allow egress for guard, installer, and pruner pods", func() {
				policy := getPolicy(kasNamespace, "allow-all-egress")
				o.Expect(policy).To(SelectPodsExpression("app", []string{"guard", "installer", "pruner"}))
				o.Expect(policy).To(AllowAllTCPEgress())
			})
		})
	})

	g.Context("[Serial] reconciliation", func() {
		g.It("should restore deleted NetworkPolicies", func() {
			policies := []*networkingv1.NetworkPolicy{
				getPolicy(kasOperatorNamespace, "default-deny"),
				getPolicy(kasOperatorNamespace, "allow-all-egress-and-metrics-ingress"),
				getPolicy(kasNamespace, "default-deny"),
				getPolicy(kasNamespace, "allow-all-egress"),
			}

			g.By("Deleting all policies and waiting for restoration")
			deleteAndWaitForAllRestored(ctx, kubeClient, policies)
		})

		g.It("should reconcile mutated NetworkPolicies", func() {
			policies := []struct{ namespace, name string }{
				{kasOperatorNamespace, "default-deny"},
				{kasOperatorNamespace, "allow-all-egress-and-metrics-ingress"},
				{kasNamespace, "default-deny"},
				{kasNamespace, "allow-all-egress"},
			}

			g.By("Mutating all policies and waiting for reconciliation")
			mutateAndWaitForAllReconciled(ctx, kubeClient, policies)
		})
	})
})
