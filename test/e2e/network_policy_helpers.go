package e2e

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

const (
	agnhostImage = "registry.k8s.io/e2e-test-images/agnhost:2.45"
)

func getClientConfigForTest() *rest.Config {
	g.GinkgoHelper()
	config, err := test.NewClientConfigForTest()
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to get client config")
	return config
}

func createTestNamespace(client corev1client.NamespaceInterface, prefix string) string {
	g.GinkgoHelper()
	name := prefix + rand.String(5)
	_, err := client.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to create namespace %s", name)
	return name
}

func ensureTestServiceAccount(ctx context.Context, client kubernetes.Interface, namespace string) string {
	g.GinkgoHelper()
	saName := "netpolicy-test-sa"
	_, err := client.CoreV1().ServiceAccounts(namespace).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return saName
	}
	if !apierrors.IsNotFound(err) {
		o.Expect(err).NotTo(o.HaveOccurred())
	}
	_, err = client.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: namespace},
	}, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to create service account %s/%s", namespace, saName)
	return saName
}

func deleteServiceAccount(ctx context.Context, client kubernetes.Interface, namespace, name string) {
	_ = client.CoreV1().ServiceAccounts(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// Reconciliation helpers

func deleteAndWaitForAllRestored(ctx context.Context, client kubernetes.Interface, policies []*networkingv1.NetworkPolicy) {
	g.GinkgoHelper()

	type policyState struct {
		expected    *networkingv1.NetworkPolicy
		originalUID types.UID
		sawDeletion bool
		confirmed   bool
	}
	states := make([]policyState, len(policies))

	for i, p := range policies {
		states[i] = policyState{expected: p, originalUID: p.UID}
		g.GinkgoWriter.Printf("deleting NetworkPolicy %s/%s (UID=%s)\n", p.Namespace, p.Name, p.UID)
		err := client.NetworkingV1().NetworkPolicies(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to delete %s/%s", p.Namespace, p.Name)
	}

	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		allRestored := true
		for i := range states {
			if states[i].confirmed {
				continue
			}
			ns, name := states[i].expected.Namespace, states[i].expected.Name
			current, err := client.NetworkingV1().NetworkPolicies(ns).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				states[i].sawDeletion = true
				allRestored = false
				continue
			}
			if err != nil {
				allRestored = false
				continue
			}
			if current.UID == states[i].originalUID && !states[i].sawDeletion {
				allRestored = false
				continue
			}
			if !equality.Semantic.DeepEqual(states[i].expected.Spec, current.Spec) {
				allRestored = false
				continue
			}
			g.GinkgoWriter.Printf("NetworkPolicy %s/%s restored\n", ns, name)
			states[i].confirmed = true
		}
		return allRestored, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "timed out waiting for NetworkPolicies to be restored")
}

func mutateAndWaitForAllReconciled(ctx context.Context, client kubernetes.Interface, policies []struct{ namespace, name string }) {
	g.GinkgoHelper()

	type mutationState struct {
		namespace, name string
		original        networkingv1.NetworkPolicySpec
		confirmed       bool
	}
	states := make([]mutationState, 0, len(policies))

	patch := []byte(`{"spec":{"podSelector":{"matchLabels":{"np-reconcile":"mutated"}}}}`)
	for _, p := range policies {
		original, err := client.NetworkingV1().NetworkPolicies(p.namespace).Get(ctx, p.name, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		g.GinkgoWriter.Printf("mutating NetworkPolicy %s/%s\n", p.namespace, p.name)
		_, err = client.NetworkingV1().NetworkPolicies(p.namespace).Patch(ctx, p.name, types.MergePatchType, patch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		states = append(states, mutationState{namespace: p.namespace, name: p.name, original: original.Spec})
	}

	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		allReconciled := true
		for i := range states {
			if states[i].confirmed {
				continue
			}
			current, err := client.NetworkingV1().NetworkPolicies(states[i].namespace).Get(ctx, states[i].name, metav1.GetOptions{})
			if err != nil {
				allReconciled = false
				continue
			}
			if !equality.Semantic.DeepEqual(states[i].original, current.Spec) {
				allReconciled = false
				continue
			}
			g.GinkgoWriter.Printf("NetworkPolicy %s/%s reconciled\n", states[i].namespace, states[i].name)
			states[i].confirmed = true
		}
		return allReconciled, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "timed out waiting for NetworkPolicies to be reconciled")
}

// Pod and connectivity helpers

func netexecPod(name, namespace string, labels map[string]string, port int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"openshift.io/required-scc": "nonroot-v2",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "netpolicy-test-sa",
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   boolPtr(true),
				RunAsUser:      int64Ptr(1001),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:  "netexec",
					Image: agnhostImage,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolPtr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						RunAsNonRoot:             boolPtr(true),
						RunAsUser:                int64Ptr(1001),
					},
					Command: []string{"/agnhost"},
					Args:    []string{"netexec", fmt.Sprintf("--http-port=%d", port)},
					Ports:   []corev1.ContainerPort{{ContainerPort: port}},
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
			},
		},
	}
}

func createServerPod(ctx context.Context, client kubernetes.Interface, namespace, name string, labels map[string]string, port int32) ([]string, func()) {
	g.GinkgoHelper()
	pod := netexecPod(name, namespace, labels, port)
	_, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(waitForPodReady(ctx, client, namespace, name)).NotTo(o.HaveOccurred())

	created, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(created.Status.PodIPs).NotTo(o.BeEmpty())

	ips := podIPs(created)
	g.GinkgoWriter.Printf("server pod %s/%s ips=%v\n", namespace, name, ips)
	return ips, func() {
		_ = client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}
}

func expectConnectivity(ctx context.Context, client kubernetes.Interface, namespace string, clientLabels map[string]string, serverIPs []string, port int32, shouldSucceed bool) {
	g.GinkgoHelper()
	for _, ip := range serverIPs {
		expectConnectivityForIP(ctx, client, namespace, clientLabels, ip, port, shouldSucceed)
	}
}

func expectConnectivityForIP(ctx context.Context, client kubernetes.Interface, namespace string, clientLabels map[string]string, serverIP string, port int32, shouldSucceed bool) {
	g.GinkgoHelper()
	target := formatIPPort(serverIP, port)
	name := fmt.Sprintf("np-client-%s", rand.String(5))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    clientLabels,
			Annotations: map[string]string{
				"openshift.io/required-scc": "nonroot-v2",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "netpolicy-test-sa",
			RestartPolicy:      corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   boolPtr(true),
				RunAsUser:      int64Ptr(1001),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:  "connect",
					Image: agnhostImage,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolPtr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						RunAsNonRoot:             boolPtr(true),
						RunAsUser:                int64Ptr(1001),
					},
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						fmt.Sprintf("while true; do if /agnhost connect --protocol=tcp --timeout=5s %s 2>/dev/null; then echo CONN_OK; else echo CONN_FAIL; fi; sleep 3; done", target),
					},
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
			},
		},
	}

	_, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	g.DeferCleanup(func() {
		_ = client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	o.Expect(waitForPodReady(ctx, client, namespace, name)).NotTo(o.HaveOccurred())

	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		tailLines := int64(1)
		raw, err := client.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
			TailLines: &tailLines,
		}).DoRaw(ctx)
		if err != nil {
			return false, nil
		}
		line := strings.TrimSpace(string(raw))
		if line == "" {
			return false, nil
		}
		return (line == "CONN_OK") == shouldSucceed, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(),
		"connectivity %s → %s expected=%t", namespace, target, shouldSucceed)
}

func waitForPodReady(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if pod.Status.Phase != corev1.PodRunning {
			return false, nil
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

// NetworkPolicy construction helpers

func defaultDenyPolicy(name, namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	}
}

func allowIngressPolicy(name, namespace string, podLabels, fromLabels map[string]string, port int32) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: podLabels},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: fromLabels}}},
				Ports: []networkingv1.NetworkPolicyPort{{Port: intstrPtr(port), Protocol: protocolPtr(corev1.ProtocolTCP)}},
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

func allowEgressPolicy(name, namespace string, podLabels, toLabels map[string]string, port int32) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: podLabels},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: toLabels}}},
				Ports: []networkingv1.NetworkPolicyPort{{Port: intstrPtr(port), Protocol: protocolPtr(corev1.ProtocolTCP)}},
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
}

// Utility helpers

func podIPs(pod *corev1.Pod) []string {
	var ips []string
	for _, podIP := range pod.Status.PodIPs {
		if podIP.IP != "" {
			ips = append(ips, podIP.IP)
		}
	}
	if len(ips) == 0 && pod.Status.PodIP != "" {
		ips = append(ips, pod.Status.PodIP)
	}
	return ips
}

func formatIPPort(ip string, port int32) string {
	if net.ParseIP(ip) != nil && strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

func protocolPtr(p corev1.Protocol) *corev1.Protocol { return &p }
func boolPtr(v bool) *bool                           { return &v }
func int64Ptr(v int64) *int64                         { return &v }

func intstrPtr(port int32) *intstr.IntOrString {
	v := intstr.FromInt32(port)
	return &v
}
