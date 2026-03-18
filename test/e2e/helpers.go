package e2e

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

const (
	agnhostImage = "registry.k8s.io/e2e-test-images/agnhost:2.45"
)

// Client configuration helpers

// getClientConfigForTest returns a config configured to connect to the api server
// This is a wrapper around test.NewClientConfigForTest that handles errors for network policy tests
func getClientConfigForTest() *rest.Config {
	g.GinkgoHelper()
	config, err := test.NewClientConfigForTest()
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to get client config")
	return config
}

// createTestNamespace creates a namespace with a random suffix and returns its name
func createTestNamespace(client corev1client.NamespaceInterface, prefix string) string {
	g.GinkgoHelper()
	name := prefix + rand.String(5)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	_, err := client.Create(context.Background(), ns, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to create test namespace %s", name)
	return name
}

// Network Policy validation helpers

func getNetworkPolicy(ctx context.Context, client kubernetes.Interface, namespace, name string) *networkingv1.NetworkPolicy {
	g.GinkgoHelper()
	policy, err := client.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred(), "failed to get NetworkPolicy %s/%s", namespace, name)
	return policy
}

func requireDefaultDenyAll(policy *networkingv1.NetworkPolicy) {
	g.GinkgoHelper()
	if len(policy.Spec.PodSelector.MatchLabels) != 0 || len(policy.Spec.PodSelector.MatchExpressions) != 0 {
		g.Fail(fmt.Sprintf("%s/%s: expected empty podSelector", policy.Namespace, policy.Name))
	}

	policyTypes := sets.New[string]()
	for _, policyType := range policy.Spec.PolicyTypes {
		policyTypes.Insert(string(policyType))
	}
	if !policyTypes.Has(string(networkingv1.PolicyTypeIngress)) || !policyTypes.Has(string(networkingv1.PolicyTypeEgress)) {
		g.Fail(fmt.Sprintf("%s/%s: expected both Ingress and Egress policyTypes, got %v", policy.Namespace, policy.Name, policy.Spec.PolicyTypes))
	}
}

func requirePodSelectorLabel(policy *networkingv1.NetworkPolicy, key, value string) {
	g.GinkgoHelper()
	actual, ok := policy.Spec.PodSelector.MatchLabels[key]
	if !ok || actual != value {
		g.Fail(fmt.Sprintf("%s/%s: expected podSelector %s=%s, got %v", policy.Namespace, policy.Name, key, value, policy.Spec.PodSelector.MatchLabels))
	}
}

func requirePodSelectorExpression(policy *networkingv1.NetworkPolicy, key string, values []string) {
	g.GinkgoHelper()
	found := false
	for _, expr := range policy.Spec.PodSelector.MatchExpressions {
		if expr.Key == key && expr.Operator == metav1.LabelSelectorOpIn {
			if sets.New[string](expr.Values...).Equal(sets.New[string](values...)) {
				found = true
				break
			}
		}
	}
	if !found {
		g.Fail(fmt.Sprintf("%s/%s: expected podSelector expression %s in %v", policy.Namespace, policy.Name, key, values))
	}
}

func requireIngressPort(policy *networkingv1.NetworkPolicy, protocol corev1.Protocol, port int32) {
	g.GinkgoHelper()
	if !hasPortInIngress(policy.Spec.Ingress, protocol, port) {
		g.Fail(fmt.Sprintf("%s/%s: expected ingress port %s/%d", policy.Namespace, policy.Name, protocol, port))
	}
}

func requireIngressAllowAll(policy *networkingv1.NetworkPolicy, port int32) {
	g.GinkgoHelper()
	if !hasIngressAllowAll(policy.Spec.Ingress, port) {
		g.Fail(fmt.Sprintf("%s/%s: expected ingress allow-all on port %d", policy.Namespace, policy.Name, port))
	}
}

func logEgressAllowAllTCP(policy *networkingv1.NetworkPolicy) {
	g.GinkgoHelper()
	if hasEgressAllowAllTCP(policy.Spec.Egress) {
		g.GinkgoWriter.Printf("networkpolicy %s/%s: egress allow-all TCP rule present\n", policy.Namespace, policy.Name)
		return
	}
	g.GinkgoWriter.Printf("networkpolicy %s/%s: no egress allow-all TCP rule\n", policy.Namespace, policy.Name)
}

func hasIngressAllowAll(rules []networkingv1.NetworkPolicyIngressRule, port int32) bool {
	for _, rule := range rules {
		if !hasPort(rule.Ports, corev1.ProtocolTCP, port) {
			continue
		}
		if len(rule.From) == 0 {
			return true
		}
	}
	return false
}

func hasEgressAllowAllTCP(rules []networkingv1.NetworkPolicyEgressRule) bool {
	for _, rule := range rules {
		if len(rule.To) != 0 {
			continue
		}
		if hasAnyTCPPort(rule.Ports) {
			return true
		}
	}
	return false
}

func hasAnyTCPPort(ports []networkingv1.NetworkPolicyPort) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		if p.Protocol != nil && *p.Protocol != corev1.ProtocolTCP {
			continue
		}
		return true
	}
	return false
}

func hasPortInIngress(rules []networkingv1.NetworkPolicyIngressRule, protocol corev1.Protocol, port int32) bool {
	for _, rule := range rules {
		if hasPort(rule.Ports, protocol, port) {
			return true
		}
	}
	return false
}

func hasPort(ports []networkingv1.NetworkPolicyPort, protocol corev1.Protocol, port int32) bool {
	for _, p := range ports {
		if p.Port == nil || p.Port.IntValue() != int(port) {
			continue
		}
		if p.Protocol == nil || *p.Protocol == protocol {
			return true
		}
	}
	return false
}

func restoreNetworkPolicy(ctx context.Context, client kubernetes.Interface, expected *networkingv1.NetworkPolicy) {
	g.GinkgoHelper()
	namespace := expected.Namespace
	name := expected.Name
	g.GinkgoWriter.Printf("deleting NetworkPolicy %s/%s\n", namespace, name)
	o.Expect(client.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, name, metav1.DeleteOptions{})).NotTo(o.HaveOccurred())
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := client.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return equality.Semantic.DeepEqual(expected.Spec, current.Spec), nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "timed out waiting for NetworkPolicy %s/%s spec to be restored", namespace, name)
	g.GinkgoWriter.Printf("NetworkPolicy %s/%s spec restored after delete\n", namespace, name)
}

func mutateAndRestoreNetworkPolicy(ctx context.Context, client kubernetes.Interface, namespace, name string) {
	g.GinkgoHelper()
	original := getNetworkPolicy(ctx, client, namespace, name)
	g.GinkgoWriter.Printf("mutating NetworkPolicy %s/%s (podSelector override)\n", namespace, name)
	patch := []byte(`{"spec":{"podSelector":{"matchLabels":{"np-reconcile":"mutated"}}}}`)
	_, err := client.NetworkingV1().NetworkPolicies(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := getNetworkPolicy(ctx, client, namespace, name)
		return equality.Semantic.DeepEqual(original.Spec, current.Spec), nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "timed out waiting for NetworkPolicy %s/%s spec to be restored", namespace, name)
	g.GinkgoWriter.Printf("NetworkPolicy %s/%s spec restored\n", namespace, name)
}

func waitForPodsReadyByLabel(ctx context.Context, client kubernetes.Interface, namespace, labelSelector string) {
	g.GinkgoHelper()
	g.GinkgoWriter.Printf("waiting for pods ready in %s with selector %s\n", namespace, labelSelector)
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return false, err
		}
		if len(pods.Items) == 0 {
			return false, nil
		}
		for _, pod := range pods.Items {
			if !isPodReady(&pod) {
				return false, nil
			}
		}
		return true, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "timed out waiting for pods in %s with selector %s to be ready", namespace, labelSelector)
}

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func logNetworkPolicyEvents(ctx context.Context, client kubernetes.Interface, namespaces []string, policyName string) {
	g.GinkgoHelper()
	found := false
	_ = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, namespace := range namespaces {
			events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				g.GinkgoWriter.Printf("unable to list events in %s: %v\n", namespace, err)
				continue
			}
			for _, event := range events.Items {
				if (event.InvolvedObject.Kind == "NetworkPolicy" && event.InvolvedObject.Name == policyName) ||
					(event.Message != "" && (event.InvolvedObject.Name == policyName || event.InvolvedObject.Kind == "NetworkPolicy")) {
					g.GinkgoWriter.Printf("event in %s: %s %s %s\n", namespace, event.Type, event.Reason, event.Message)
					found = true
				}
			}
		}
		if found {
			return true, nil
		}
		g.GinkgoWriter.Printf("no NetworkPolicy events yet for %s (namespaces: %v)\n", policyName, namespaces)
		return false, nil
	})
	if !found {
		g.GinkgoWriter.Printf("no NetworkPolicy events observed for %s (best-effort)\n", policyName)
	}
}

func logNetworkPolicySummary(label string, policy *networkingv1.NetworkPolicy) {
	g.GinkgoWriter.Printf("networkpolicy %s namespace=%s name=%s podSelector=%v policyTypes=%v ingress=%d egress=%d\n",
		label,
		policy.Namespace,
		policy.Name,
		policy.Spec.PodSelector.MatchLabels,
		policy.Spec.PolicyTypes,
		len(policy.Spec.Ingress),
		len(policy.Spec.Egress),
	)
}

func logNetworkPolicyDetails(label string, policy *networkingv1.NetworkPolicy) {
	g.GinkgoHelper()
	g.GinkgoWriter.Printf("networkpolicy %s details:\n", label)
	g.GinkgoWriter.Printf("  podSelector=%v policyTypes=%v\n", policy.Spec.PodSelector.MatchLabels, policy.Spec.PolicyTypes)
	for i, rule := range policy.Spec.Ingress {
		g.GinkgoWriter.Printf("  ingress[%d]: ports=%s from=%s\n", i, formatPorts(rule.Ports), formatPeers(rule.From))
	}
	for i, rule := range policy.Spec.Egress {
		g.GinkgoWriter.Printf("  egress[%d]: ports=%s to=%s\n", i, formatPorts(rule.Ports), formatPeers(rule.To))
	}
}

func formatPorts(ports []networkingv1.NetworkPolicyPort) string {
	if len(ports) == 0 {
		return "[]"
	}
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		if p.Port == nil {
			out = append(out, fmt.Sprintf("%s:any", proto))
			continue
		}
		out = append(out, fmt.Sprintf("%s:%s", proto, p.Port.String()))
	}
	return fmt.Sprintf("[%s]", strings.Join(out, ", "))
}

func formatPeers(peers []networkingv1.NetworkPolicyPeer) string {
	if len(peers) == 0 {
		return "[]"
	}
	out := make([]string, 0, len(peers))
	for _, peer := range peers {
		ns := formatSelector(peer.NamespaceSelector)
		pod := formatSelector(peer.PodSelector)
		if ns == "" && pod == "" {
			out = append(out, "{}")
			continue
		}
		out = append(out, fmt.Sprintf("ns=%s pod=%s", ns, pod))
	}
	return fmt.Sprintf("[%s]", strings.Join(out, ", "))
}

func formatSelector(sel *metav1.LabelSelector) string {
	if sel == nil {
		return ""
	}
	if len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		return "{}"
	}
	return fmt.Sprintf("labels=%v exprs=%v", sel.MatchLabels, sel.MatchExpressions)
}

// Network Policy enforcement helpers

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
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   boolptr(true),
				RunAsUser:      int64ptr(1001),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:  "netexec",
					Image: agnhostImage,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolptr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						RunAsNonRoot:             boolptr(true),
						RunAsUser:                int64ptr(1001),
					},
					Command: []string{"/agnhost"},
					Args:    []string{"netexec", fmt.Sprintf("--http-port=%d", port)},
					Ports: []corev1.ContainerPort{
						{ContainerPort: port},
					},
				},
			},
		},
	}
}

func createServerPod(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string, labels map[string]string, port int32) ([]string, func()) {
	g.GinkgoHelper()

	g.GinkgoWriter.Printf("creating server pod %s/%s port=%d labels=%v\n", namespace, name, port, labels)
	pod := netexecPod(name, namespace, labels, port)
	_, err := kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(waitForPodReady(ctx, kubeClient, namespace, name)).NotTo(o.HaveOccurred())

	created, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(created.Status.PodIPs).NotTo(o.BeEmpty())

	ips := podIPs(created)
	g.GinkgoWriter.Printf("server pod %s/%s ips=%v\n", namespace, name, ips)

	return ips, func() {
		g.GinkgoWriter.Printf("deleting server pod %s/%s\n", namespace, name)
		_ = kubeClient.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}
}

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

func expectConnectivityForIP(ctx context.Context, kubeClient kubernetes.Interface, namespace string, clientLabels map[string]string, serverIP string, port int32, shouldSucceed bool) {
	g.GinkgoHelper()

	podName, cleanup, err := createConnectivityClientPod(ctx, kubeClient, namespace, clientLabels, serverIP, port)
	o.Expect(err).NotTo(o.HaveOccurred())
	g.DeferCleanup(cleanup)

	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		succeeded, err := readConnectivityResult(ctx, kubeClient, namespace, podName)
		if err != nil {
			g.GinkgoWriter.Printf("waiting for connectivity result: %v\n", err)
			return false, nil
		}
		return succeeded == shouldSucceed, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred())
	g.GinkgoWriter.Printf("connectivity %s/%s expected=%t\n", namespace, formatIPPort(serverIP, port), shouldSucceed)
}

func expectConnectivity(ctx context.Context, kubeClient kubernetes.Interface, namespace string, clientLabels map[string]string, serverIPs []string, port int32, shouldSucceed bool) {
	g.GinkgoHelper()

	for _, ip := range serverIPs {
		family := "IPv4"
		if isIPv6(ip) {
			family = "IPv6"
		}
		g.GinkgoWriter.Printf("checking %s connectivity %s -> %s expected=%t\n", family, namespace, formatIPPort(ip, port), shouldSucceed)
		expectConnectivityForIP(ctx, kubeClient, namespace, clientLabels, ip, port, shouldSucceed)
	}
}

func createConnectivityClientPod(ctx context.Context, kubeClient kubernetes.Interface, namespace string, labels map[string]string, serverIP string, port int32) (string, func(), error) {
	name := fmt.Sprintf("np-client-%s", rand.String(5))
	target := formatIPPort(serverIP, port)

	g.GinkgoWriter.Printf("creating client pod %s/%s to probe %s\n", namespace, name, target)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"openshift.io/required-scc": "nonroot-v2",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   boolptr(true),
				RunAsUser:      int64ptr(1001),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:  "connect",
					Image: agnhostImage,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolptr(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						RunAsNonRoot:             boolptr(true),
						RunAsUser:                int64ptr(1001),
					},
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						fmt.Sprintf("while true; do if /agnhost connect --protocol=tcp --timeout=5s %s 2>/dev/null; then echo CONN_OK; else echo CONN_FAIL; fi; sleep 3; done", target),
					},
				},
			},
		},
	}

	_, err := kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", nil, err
	}

	if err := waitForPodReady(ctx, kubeClient, namespace, name); err != nil {
		_ = kubeClient.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		return "", nil, fmt.Errorf("client pod %s/%s never became ready: %w", namespace, name, err)
	}

	cleanup := func() {
		g.GinkgoWriter.Printf("deleting client pod %s/%s\n", namespace, name)
		_ = kubeClient.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}

	return name, cleanup, nil
}

func readConnectivityResult(ctx context.Context, kubeClient kubernetes.Interface, namespace, podName string) (bool, error) {
	tailLines := int64(1)
	raw, err := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &tailLines,
	}).DoRaw(ctx)
	if err != nil {
		return false, err
	}

	line := strings.TrimSpace(string(raw))
	if line == "" {
		return false, fmt.Errorf("no connectivity result yet from pod %s/%s", namespace, podName)
	}

	g.GinkgoWriter.Printf("client pod %s/%s result=%s\n", namespace, podName, line)
	return line == "CONN_OK", nil
}

func ingressAllowsFromNamespace(policy *networkingv1.NetworkPolicy, namespace string, labels map[string]string, port int32) bool {
	for _, rule := range policy.Spec.Ingress {
		if !ruleAllowsPort(rule.Ports, port) {
			continue
		}
		if len(rule.From) == 0 {
			return true
		}
		for _, peer := range rule.From {
			if peer.NamespaceSelector != nil {
				if nsMatch(peer.NamespaceSelector, namespace) && podMatch(peer.PodSelector, labels) {
					return true
				}
				continue
			}
			if podMatch(peer.PodSelector, labels) {
				return true
			}
		}
	}
	return false
}

func nsMatch(selector *metav1.LabelSelector, namespace string) bool {
	if selector == nil {
		return true
	}
	if selector.MatchLabels != nil {
		if selector.MatchLabels["kubernetes.io/metadata.name"] == namespace {
			return true
		}
	}
	for _, expr := range selector.MatchExpressions {
		if expr.Key != "kubernetes.io/metadata.name" {
			continue
		}
		if expr.Operator != metav1.LabelSelectorOpIn {
			continue
		}
		if slices.Contains(expr.Values, namespace) {
			return true
		}
	}
	return false
}

func podMatch(selector *metav1.LabelSelector, labels map[string]string) bool {
	if selector == nil {
		return true
	}
	for key, value := range selector.MatchLabels {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func ruleAllowsPort(ports []networkingv1.NetworkPolicyPort, port int32) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		if p.Port == nil {
			return true
		}
		if p.Port.Type == intstr.Int && p.Port.IntVal == port {
			return true
		}
	}
	return false
}

func waitForPodReady(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
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
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &metav1.LabelSelector{MatchLabels: fromLabels}},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{Port: &intstr.IntOrString{Type: intstr.Int, IntVal: port}, Protocol: protocolPtr(corev1.ProtocolTCP)},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

func allowEgressPolicy(name, namespace string, podLabels, toLabels map[string]string, port int32) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: podLabels},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &metav1.LabelSelector{MatchLabels: toLabels}},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{Port: &intstr.IntOrString{Type: intstr.Int, IntVal: port}, Protocol: protocolPtr(corev1.ProtocolTCP)},
					},
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
}

// Utility helpers

func isIPv6(ip string) bool {
	return net.ParseIP(ip) != nil && strings.Contains(ip, ":")
}

func formatIPPort(ip string, port int32) string {
	if isIPv6(ip) {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

func protocolPtr(protocol corev1.Protocol) *corev1.Protocol {
	return &protocol
}

func boolptr(value bool) *bool {
	return &value
}

func int64ptr(value int64) *int64 {
	return &value
}
