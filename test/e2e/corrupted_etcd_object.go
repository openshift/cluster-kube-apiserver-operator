package e2e

import (
	"bytes"
	"context"
	"fmt"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configclientset "github.com/openshift/client-go/config/clientset/versioned"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	libgotest "github.com/openshift/library-go/test/library"
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// This test covers OCPBUGS-59625: with an undecodable object stored in etcd in a
// namespace that the kube-apiserver operator does not observe, forcing a revision
// change must not cause runaway revision creation or InstallerControllerDegraded.
// Before the fix, the staticpod controllers' informer-backed cached getters could
// not observe the freshly created revision-scoped resources while the corrupted
// key existed, so the revision controller kept creating new revisions.
var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery][Suite:corrupted-etcd-object][Skipped:HyperShift][Skipped:MicroShift][Disruptive] kube-apiserver operator tolerates corrupted etcd objects in unrelated namespaces", func() {
	const testSecretName = "test-secret"

	var (
		kubeConfig     *rest.Config
		kubeClient     *kubernetes.Clientset
		operatorClient *operatorclientset.Clientset
		configClient   *configclientset.Clientset
		testNamespace  string
	)

	g.BeforeEach(func() {
		var err error
		kubeConfig, err = libgotest.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred())

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		configClient, err = configclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	g.AfterEach(func() {
		// a bounded context independent of the spec context, so cleanup can still
		// remove the corrupted etcd key after the operation context expired
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		g.By("Removing the corrupted etcd key")
		removeCorruptedEtcdKey(cleanupCtx, kubeClient, kubeConfig, corruptedEtcdPath(testNamespace, testSecretName))

		g.By("Deleting the test namespace")
		err := kubeClient.CoreV1().Namespaces().Delete(cleanupCtx, testNamespace, metav1.DeleteOptions{})
		if err != nil {
			o.Expect(err.Error()).To(o.ContainSubstring("not found"))
		}

		g.By("Waiting for kube-apiserver to recover")
		err = testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
		)
		o.Expect(err).NotTo(o.HaveOccurred(), "kube-apiserver did not recover after cleanup")
	})

	g.It("must install a forced revision without churn while a foreign-namespace etcd key is corrupted [Timeout:30m][Late]", func(ctx g.SpecContext) {
		g.By("Creating the test namespace")
		ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-corrupted-etcd-object-"},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		testNamespace = ns.Name

		g.By("Corrupting a secret key directly in etcd")
		corruptEtcdKey(ctx, kubeClient, kubeConfig, corruptedEtcdPath(testNamespace, testSecretName))

		g.By("Verifying the corrupted object is undecodable through the API")
		_, err = kubeClient.CoreV1().Secrets(testNamespace).Get(ctx, testSecretName, metav1.GetOptions{})
		if err == nil {
			g.Skip("the corrupted key did not produce an undecodable object; the corruption is not effective on this cluster")
		}
		o.Expect(err.Error()).To(o.ContainSubstring("illegal base64 data"), "unexpected error for the corrupted object: %v", err)

		g.By("Reading the baseline revision")
		baselineRevision, err := latestAvailableRevision(ctx, operatorClient)
		o.Expect(err).NotTo(o.HaveOccurred())

		// the namespace name is unique per execution, so the forced-redeployment
		// reason changes the operator input on every run of this test
		g.By("Forcing a revision change")
		forceRedeployment(ctx, operatorClient, testNamespace)

		g.By("Waiting for the forced revision to be created")
		o.Eventually(func() (int32, error) {
			return latestAvailableRevision(ctx, operatorClient)
		}, 2*time.Minute, 5*time.Second).Should(o.BeNumerically(">", baselineRevision),
			"the forced revision was never created")

		g.By("Waiting for every API server node to run the forced revision")
		o.Eventually(func() error {
			return allAPIServerPodsAtRevision(ctx, kubeClient, baselineRevision+1)
		}, 15*time.Minute, 15*time.Second).Should(o.Succeed(),
			"the forced revision was never rolled out to all API server nodes")

		g.By("Waiting for the API server to stabilize on the forced revision")
		err = testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
		)
		o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize on the forced revision")

		g.By("Verifying no revision churn happened")
		finalRevision, err := latestAvailableRevision(ctx, operatorClient)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(finalRevision).To(o.Equal(baselineRevision+1),
			"expected exactly one new revision (baseline %d), but ended at %d — the operator is churning revisions", baselineRevision, finalRevision)

		g.By("Verifying the cluster operator is not degraded")
		co, err := configClient.ConfigV1().ClusterOperators().Get(ctx, "kube-apiserver", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		for _, condition := range co.Status.Conditions {
			if condition.Type == configv1.OperatorDegraded {
				o.Expect(condition.Status).To(o.Equal(configv1.ConditionFalse),
					"kube-apiserver degraded while a foreign-namespace etcd key was corrupted: %s", condition.Message)
			}
		}
	})
})

func corruptedEtcdPath(namespace, secretName string) string {
	return "/kubernetes.io/secrets/" + namespace + "/" + secretName
}

// allAPIServerPodsAtRevision checks that every apiserver=true pod in the operand
// namespace reports the given revision via its revision label, the same mechanism
// WaitForAPIServerToStabilizeOnTheSameRevision uses to compare revisions.
func allAPIServerPodsAtRevision(ctx context.Context, kubeClient *kubernetes.Clientset, revision int32) error {
	pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: "apiserver=true"})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no API server pods found with label apiserver=true")
	}
	wantRevision := fmt.Sprintf("%d", revision)
	for _, pod := range pods.Items {
		if pod.Labels["revision"] != wantRevision {
			return fmt.Errorf("pod %s is at revision %q, want %d", pod.Name, pod.Labels["revision"], revision)
		}
	}
	return nil
}

func latestAvailableRevision(ctx context.Context, operatorClient *operatorclientset.Clientset) (int32, error) {
	kas, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	return kas.Status.LatestAvailableRevision, nil
}

func forceRedeployment(ctx context.Context, operatorClient *operatorclientset.Clientset, reason string) {
	kas, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	kas.Spec.ForceRedeploymentReason = reason
	_, err = operatorClient.OperatorV1().KubeAPIServers().Update(ctx, kas, metav1.UpdateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
}

// etcdPodExec runs the given command inside one etcd pod and returns its combined output.
func etcdPodExec(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, command ...string) (string, error) {
	etcdPods, err := kubeClient.CoreV1().Pods("openshift-etcd").List(ctx, metav1.ListOptions{LabelSelector: "app=etcd"})
	if err != nil {
		return "", err
	}
	if len(etcdPods.Items) == 0 {
		return "", fmt.Errorf("no etcd pods found")
	}
	etcdPod := etcdPods.Items[0].Name

	request := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(etcdPod).
		Namespace("openshift-etcd").
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeConfig, "POST", request.URL())
	if err != nil {
		return "", err
	}
	// the exec stream pumps stdout and stderr concurrently, so they must not share
	// a buffer; combine them after the stream has completed
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.String() + stderr.String(), err
}

func corruptEtcdKey(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPath string) {
	output, err := etcdPodExec(ctx, kubeClient, kubeConfig, "etcdctl", "put", etcdPath, "corrupted-data")
	o.Expect(err).NotTo(o.HaveOccurred(), "etcdctl put failed: %s", output)
	o.Expect(output).To(o.ContainSubstring("OK"), "unexpected etcdctl put output: %s", output)
}

func removeCorruptedEtcdKey(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPath string) {
	output, err := etcdPodExec(ctx, kubeClient, kubeConfig, "etcdctl", "del", etcdPath)
	o.Expect(err).NotTo(o.HaveOccurred(), "etcdctl del failed: %s", output)
}
