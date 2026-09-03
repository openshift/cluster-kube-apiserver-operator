package e2e

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
)

// warningMessage is the exact message emitted by the
// rolebinding-serviceaccount-token-escalation-{role,clusterrole} ValidatingAdmissionPolicies.
const warningMessage = "Binding the create verb on serviceaccounts/tokens to an entity allows that entity to impersonate any existing or future serviceaccount within the same namespace. This may allow privilege escalation (to the cluster level) when the serviceaccount has broader permissions than the impersonating entity."

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Serial][Operator][Feature:ServiceAccountTokenEscalationWarning] should warn when a RoleBinding grants serviceaccounts/token create", func() {
		testRoleBindingTokenEscalationWarning()
	})
})

// warningRecorder captures admission warning headers returned to the client.
type warningRecorder struct {
	mu       sync.Mutex
	warnings []string
}

func (w *warningRecorder) HandleWarningHeader(code int, agent, message string) {
	if message == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warnings = append(w.warnings, message)
}

func (w *warningRecorder) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warnings = nil
}

func (w *warningRecorder) has(message string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Contains(w.warnings, message)
}

func testRoleBindingTokenEscalationWarning() {
	ctx := context.Background()

	recorder := &warningRecorder{}
	kubeConfig := getClientConfigForTest()
	kubeConfig.WarningHandler = recorder
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	nsName := createTestNamespace(kubeClient.CoreV1().Namespaces(), "rb-token-escalation-")
	g.DeferCleanup(func() {
		g.GinkgoWriter.Printf("deleting test namespace %s\n", nsName)
		_ = kubeClient.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
	})

	// tokenCreateRules grants the equivalent of "serviceaccounts/token create".
	tokenCreateRules := []rbacv1.PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"serviceaccounts/token"},
		Verbs:     []string{"create"},
	}}
	// benignRules grants something harmless (and, notably, plain serviceaccounts,
	// which must NOT trigger the warning since it does not cover the token subresource).
	benignRules := []rbacv1.PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"serviceaccounts", "configmaps"},
		Verbs:     []string{"get", "list", "watch"},
	}}

	g.By("Creating a Role and ClusterRole that grant serviceaccounts/token create")
	dangerousRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "token-minter", Namespace: nsName},
		Rules:      tokenCreateRules,
	}
	_, err = kubeClient.RbacV1().Roles(nsName).Create(ctx, dangerousRole, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	benignRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "benign-reader", Namespace: nsName},
		Rules:      benignRules,
	}
	_, err = kubeClient.RbacV1().Roles(nsName).Create(ctx, benignRole, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	dangerousClusterRoleName := "token-minter-" + rand.String(5)
	dangerousClusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: dangerousClusterRoleName},
		Rules:      tokenCreateRules,
	}
	_, err = kubeClient.RbacV1().ClusterRoles().Create(ctx, dangerousClusterRole, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	g.DeferCleanup(func() {
		_ = kubeClient.RbacV1().ClusterRoles().Delete(ctx, dangerousClusterRoleName, metav1.DeleteOptions{})
	})

	userSubject := rbacv1.Subject{Kind: rbacv1.UserKind, Name: "alice", APIGroup: rbacv1.GroupName}
	saSubject := rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "default", Namespace: nsName}

	g.By("Creating a RoleBinding to a user for the dangerous Role and expecting a warning")
	expectWarningOnRoleBinding(ctx, kubeClient, recorder, nsName,
		roleRef("Role", dangerousRole.Name), userSubject, true)

	g.By("Creating a RoleBinding to a user for the dangerous ClusterRole and expecting a warning")
	expectWarningOnRoleBinding(ctx, kubeClient, recorder, nsName,
		roleRef("ClusterRole", dangerousClusterRoleName), userSubject, true)

	g.By("Creating a RoleBinding to a service account for the dangerous Role and expecting a warning")
	expectWarningOnRoleBinding(ctx, kubeClient, recorder, nsName,
		roleRef("Role", dangerousRole.Name), saSubject, true)

	g.By("Creating a RoleBinding to a user for a benign Role and expecting no warning")
	expectWarningOnRoleBinding(ctx, kubeClient, recorder, nsName,
		roleRef("Role", benignRole.Name), userSubject, false)
}

func roleRef(kind, name string) rbacv1.RoleRef {
	return rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: kind, Name: name}
}

// expectWarningOnRoleBinding creates a uniquely-named RoleBinding and asserts whether the
// escalation warning was returned. When expectWarning is true it retries with fresh
// RoleBindings to tolerate the delay between the policy being applied and enforced.
func expectWarningOnRoleBinding(ctx context.Context, kubeClient kubernetes.Interface, recorder *warningRecorder, ns string, ref rbacv1.RoleRef, subject rbacv1.Subject, expectWarning bool) {
	g.GinkgoHelper()

	create := func() error {
		recorder.reset()
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb-" + rand.String(5), Namespace: ns},
			RoleRef:    ref,
			Subjects:   []rbacv1.Subject{subject},
		}
		if _, err := kubeClient.RbacV1().RoleBindings(ns).Create(ctx, rb, metav1.CreateOptions{}); err != nil {
			return err
		}
		return nil
	}

	if expectWarning {
		o.Eventually(func() (bool, error) {
			if err := create(); err != nil {
				return false, err
			}
			return recorder.has(warningMessage), nil
		}, 60*time.Second, 3*time.Second).Should(o.BeTrue(),
			fmt.Sprintf("expected escalation warning for %s/%s", ref.Kind, ref.Name))
		return
	}

	o.Expect(create()).NotTo(o.HaveOccurred())
	o.Expect(recorder.has(warningMessage)).To(o.BeFalse(),
		fmt.Sprintf("did not expect escalation warning for %s/%s subject %s", ref.Kind, ref.Name, subject.Kind))
}
