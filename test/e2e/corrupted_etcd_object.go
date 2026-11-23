package e2e

import (
	"bytes"
	"context"
	"errors"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		kubeConfig                        *rest.Config
		kubeClient                        *kubernetes.Clientset
		operatorClient                    *operatorclientset.Clientset
		configClient                      *configclientset.Clientset
		testNamespace                     string
		originalForceRedeploymentReason   string
		forceRedeploymentReasonWasChanged bool
	)

	g.BeforeEach(func() {
		var err error
		kubeConfig, err = libgotest.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create test client config")

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create Kubernetes client")

		operatorClient, err = operatorclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create operator client")

		configClient, err = configclientset.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create config client")
	})

	g.AfterEach(func() {
		defer func() {
			testNamespace = ""
			originalForceRedeploymentReason = ""
			forceRedeploymentReasonWasChanged = false
		}()

		// BeforeEach or namespace creation can fail before cleanup has anything to
		// remove. In that case there is no etcd key to restore and no namespace to
		// delete.
		if kubeClient == nil || (testNamespace == "" && !forceRedeploymentReasonWasChanged) {
			return
		}

		// a bounded context independent of the spec context, so cleanup can still
		// remove the corrupted etcd key after the operation context expired
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		var cleanupErrors []error

		if testNamespace != "" {
			g.By("Removing the corrupted etcd key")
			// retry: a transient pod-list or exec failure must not abort cleanup and
			// leave the corrupted key in etcd
			if err := removeCorruptedEtcdKeyWithRetry(cleanupCtx, kubeClient, kubeConfig, corruptedEtcdPath(testNamespace, testSecretName)); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to remove the corrupted etcd key: %w", err))
			}

			g.By("Deleting the test namespace")
			err := kubeClient.CoreV1().Namespaces().Delete(cleanupCtx, testNamespace, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("failed to delete test namespace %s: %w", testNamespace, err))
			}
		}

		if forceRedeploymentReasonWasChanged {
			g.By("Restoring the original force redeployment reason")
			if err := restoreForceRedeploymentReason(cleanupCtx, operatorClient, originalForceRedeploymentReason); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}

		g.By("Waiting for kube-apiserver to recover")
		err := testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
		)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("kube-apiserver did not recover after cleanup: %w", err))
		}
		o.Expect(errors.Join(cleanupErrors...)).NotTo(o.HaveOccurred(), "cleanup failed")
	})

	// This remains a blocking e2e because a regression can leave kube-apiserver
	// degraded and prevent control-plane rollouts. Keep it as one scenario: the
	// corrupted etcd key must stay present through rollout and health checks, while
	// AfterEach removes it and restores the original force redeployment reason.
	g.It("must install a forced revision without churn while a foreign-namespace etcd key is corrupted [Timeout:30m][Late]", func(ctx g.SpecContext) {
		g.By("Reading the baseline revision and control-plane nodes")
		baselineRevision, err := latestAvailableRevision(ctx, operatorClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to read the baseline revision")
		expectedAPIServerNodes, err := apiServerNodeNames(ctx, kubeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to list control-plane nodes")

		g.By("Creating the test namespace")
		ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-corrupted-etcd-object-"},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to create test namespace")
		testNamespace = ns.Name

		g.By("Corrupting a secret key directly in etcd")
		corruptEtcdKey(ctx, kubeClient, kubeConfig, corruptedEtcdPath(testNamespace, testSecretName))

		g.By("Verifying the corrupted object is undecodable through the API")
		_, err = kubeClient.CoreV1().Secrets(testNamespace).Get(ctx, testSecretName, metav1.GetOptions{})
		if err == nil {
			g.Skip("the corrupted key did not produce an undecodable object; the corruption is not effective on this cluster")
		}
		o.Expect(err.Error()).To(o.ContainSubstring("illegal base64 data"), "unexpected error for the corrupted object: %v", err)

		g.By("Confirming the unrelated corruption does not trigger a revision")
		o.Consistently(func() (int32, error) {
			return latestAvailableRevision(ctx, operatorClient)
		}, 30*time.Second, 5*time.Second).Should(o.Equal(baselineRevision),
			"the foreign-namespace corrupted object triggered a revision before redeployment was forced")

		// the namespace name is unique per execution, so the forced-redeployment
		// reason changes the operator input on every run of this test
		g.By("Forcing a revision change")
		originalForceRedeploymentReason, forceRedeploymentReasonWasChanged, err = forceRedeployment(ctx, operatorClient, testNamespace)
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to force kube-apiserver redeployment")

		g.By("Waiting for the forced revision to be created")
		o.Eventually(func() (int32, error) {
			return latestAvailableRevision(ctx, operatorClient)
		}, 2*time.Minute, 5*time.Second).Should(o.BeNumerically(">", baselineRevision),
			"the forced revision was never created")

		g.By("Waiting for every API server node to run the forced revision")
		o.Eventually(func() error {
			return allAPIServerPodsAtRevision(ctx, kubeClient, baselineRevision+1, expectedAPIServerNodes)
		}, 15*time.Minute, 15*time.Second).Should(o.Succeed(),
			"the forced revision was never rolled out to all API server nodes")

		g.By("Waiting for the API server to stabilize on the forced revision")
		err = testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(
			g.GinkgoT(),
			kubeClient.CoreV1().Pods(operatorclient.TargetNamespace),
		)
		o.Expect(err).NotTo(o.HaveOccurred(), "API server did not stabilize on the forced revision")

		g.By("Verifying no revision churn happened")
		o.Consistently(func() (int32, error) {
			return latestAvailableRevision(ctx, operatorClient)
		}, 90*time.Second, 5*time.Second).Should(o.Equal(baselineRevision+1),
			"expected exactly one new revision (baseline %d); the operator kept creating revisions", baselineRevision)

		g.By("Verifying the cluster operator is not degraded")
		o.Eventually(func() error {
			co, err := configClient.ConfigV1().ClusterOperators().Get(ctx, "kube-apiserver", metav1.GetOptions{})
			if err != nil {
				return err
			}
			for _, condition := range co.Status.Conditions {
				if condition.Type == configv1.OperatorDegraded {
					if condition.Status == configv1.ConditionFalse {
						return nil
					}
					return fmt.Errorf("kube-apiserver is degraded: %s", condition.Message)
				}
			}
			return fmt.Errorf("kube-apiserver ClusterOperator has no Degraded condition")
		}, 2*time.Minute, 5*time.Second).Should(o.Succeed(), "kube-apiserver did not recover while the foreign-namespace etcd key was corrupted")
	})
})

func corruptedEtcdPath(namespace, secretName string) string {
	return "/kubernetes.io/secrets/" + namespace + "/" + secretName
}

func apiServerNodeNames(ctx context.Context, kubeClient *kubernetes.Clientset) (map[string]struct{}, error) {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/master"})
	if err != nil {
		return nil, err
	}
	if len(nodes.Items) == 0 {
		return nil, fmt.Errorf("no control-plane nodes found")
	}

	result := make(map[string]struct{}, len(nodes.Items))
	for _, node := range nodes.Items {
		result[node.Name] = struct{}{}
	}
	return result, nil
}

// allAPIServerPodsAtRevision checks that every control-plane node has an
// apiserver=true pod in the operand namespace and that every such pod reports the
// requested revision. Checking the expected node set prevents a missing pod from
// making the remaining pods appear converged.
func allAPIServerPodsAtRevision(ctx context.Context, kubeClient *kubernetes.Clientset, revision int32, expectedNodes map[string]struct{}) error {
	pods, err := kubeClient.CoreV1().Pods(operatorclient.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: "apiserver=true"})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no API server pods found with label apiserver=true")
	}
	wantRevision := fmt.Sprintf("%d", revision)
	seenNodes := make(map[string]struct{}, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" {
			return fmt.Errorf("pod %s is not assigned to a node", pod.Name)
		}
		if pod.Labels["revision"] != wantRevision {
			return fmt.Errorf("pod %s is at revision %q, want %d", pod.Name, pod.Labels["revision"], revision)
		}
		seenNodes[pod.Spec.NodeName] = struct{}{}
	}
	for nodeName := range expectedNodes {
		if _, ok := seenNodes[nodeName]; !ok {
			return fmt.Errorf("no API server pod found on control-plane node %s", nodeName)
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

func forceRedeployment(ctx context.Context, operatorClient *operatorclientset.Clientset, reason string) (string, bool, error) {
	kas, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return "", false, fmt.Errorf("failed to read kube-apiserver configuration before forcing redeployment: %w", err)
	}
	originalReason := kas.Spec.ForceRedeploymentReason
	if originalReason == reason {
		return originalReason, false, nil
	}
	kas.Spec.ForceRedeploymentReason = reason
	_, err = operatorClient.OperatorV1().KubeAPIServers().Update(ctx, kas, metav1.UpdateOptions{})
	if err != nil {
		return originalReason, true, fmt.Errorf("failed to force kube-apiserver redeployment: %w", err)
	}
	return originalReason, true, nil
}

func restoreForceRedeploymentReason(ctx context.Context, operatorClient *operatorclientset.Clientset, reason string) error {
	kas, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read kube-apiserver configuration before restoring force redeployment reason: %w", err)
	}
	if kas.Spec.ForceRedeploymentReason == reason {
		return nil
	}
	kas.Spec.ForceRedeploymentReason = reason
	_, err = operatorClient.OperatorV1().KubeAPIServers().Update(ctx, kas, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to restore the original force redeployment reason: %w", err)
	}
	return nil
}

// etcdPodExec tries each etcd pod with a bounded exec attempt and returns the
// combined output from the first successful attempt.
func etcdPodExec(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, command ...string) (string, error) {
	etcdPods, err := kubeClient.CoreV1().Pods("openshift-etcd").List(ctx, metav1.ListOptions{LabelSelector: "app=etcd"})
	if err != nil {
		return "", err
	}
	if len(etcdPods.Items) == 0 {
		return "", fmt.Errorf("no etcd pods found")
	}

	var execErrors []error
	for _, etcdPod := range etcdPods.Items {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		output, err := etcdPodExecOnPod(attemptCtx, kubeClient, kubeConfig, etcdPod.Name, command...)
		cancel()
		if err == nil {
			return output, nil
		}
		execErrors = append(execErrors, fmt.Errorf("pod %s: %w", etcdPod.Name, err))
	}

	return "", fmt.Errorf("etcdctl exec failed on all %d etcd pods: %w", len(etcdPods.Items), errors.Join(execErrors...))
}

// etcdPodExecOnPod runs the given command in the named etcd pod.
func etcdPodExecOnPod(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPod string, command ...string) (string, error) {

	// the etcd pod has multiple containers; the etcdctl container is first in the
	// pod spec and is designed to run etcdctl with the client env preset — an exec
	// request without a container name is rejected by the API server
	request := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(etcdPod).
		Namespace("openshift-etcd").
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: "etcdctl",
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
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
	if err != nil {
		// StreamWithContext can return on context cancellation before its stream
		// goroutines have finished writing to the buffers.
		return "", err
	}
	return stdout.String() + stderr.String(), nil
}

func corruptEtcdKey(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPath string) {
	output, err := etcdPodExec(ctx, kubeClient, kubeConfig, "etcdctl", "put", etcdPath, "corrupted-data")
	o.Expect(err).NotTo(o.HaveOccurred(), "etcdctl put failed for %s", etcdPath)
	o.Expect(output).To(o.ContainSubstring("OK"), "unexpected etcdctl put output: %s", output)
}

func removeCorruptedEtcdKey(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPath string) error {
	_, err := etcdPodExec(ctx, kubeClient, kubeConfig, "etcdctl", "del", etcdPath)
	if err != nil {
		return fmt.Errorf("etcdctl del failed: %w", err)
	}
	return nil
}

func removeCorruptedEtcdKeyWithRetry(ctx context.Context, kubeClient *kubernetes.Clientset, kubeConfig *rest.Config, etcdPath string) error {
	const retryTimeout = 2 * time.Minute
	const retryInterval = 10 * time.Second

	retryCtx, cancel := context.WithTimeout(ctx, retryTimeout)
	defer cancel()

	for {
		lastErr := removeCorruptedEtcdKey(retryCtx, kubeClient, kubeConfig, etcdPath)
		if lastErr == nil {
			return nil
		}

		retryTimer := time.NewTimer(retryInterval)
		select {
		case <-retryCtx.Done():
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("cleanup context ended before removing the corrupted etcd key: %w (last attempt: %v)", err, lastErr)
			}
			return fmt.Errorf("timed out after %s removing the corrupted etcd key: %w", retryTimeout, lastErr)
		case <-retryTimer.C:
		}
	}
}
