package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

const (
	operatorNamespace = "openshift-kube-apiserver-operator"
	operatorName      = "kube-apiserver-operator"
	targetNamespace   = "openshift-kube-apiserver"
	secretName        = "kubelet-client"

	cvoNamespace = "openshift-cluster-version"
	cvoName      = "cluster-version-operator"
)

var _ = g.Describe("[Operator][Serial] Cert Rotation Sidecar", func() {
	var (
		kubeClient   *kubernetes.Clientset
		configClient *configclient.ConfigV1Client
		ctx          context.Context
	)

	g.BeforeEach(func() {
		kubeConfig, err := test.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred())

		kubeClient, err = kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		configClient, err = configclient.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx = context.Background()

		// Pause CVO reconciliation
		g.By("Pausing CVO reconciliation")
		pauseCVO(kubeClient, configClient, ctx)
	})

	g.AfterEach(func() {
		// Resume CVO reconciliation
		g.By("Resuming CVO reconciliation")
		resumeCVO(kubeClient, configClient, ctx)

		// Scale operator back up for cleanup
		g.By("Scaling operator back up for cleanup")
		scaleDeployment(kubeClient, ctx, operatorNamespace, operatorName, 1)
	})

	g.It("should not restore metadata annotations when operator is down", func() {
		g.By("Scenario A: operator down, removing metadata should not be re-added by sidecar")

		// Scale down the operator
		scaleDeployment(kubeClient, ctx, operatorNamespace, operatorName, 0)

		// Get the current secret and save the cert data
		sec, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		dataBefore := base64.StdEncoding.EncodeToString(sec.Data["tls.crt"])

		// Remove metadata annotations
		patchAnnotationsOnSecret(kubeClient, ctx, targetNamespace, secretName, map[string]*string{
			"openshift.io/owning-component": nil,
			"openshift.io/description":      nil,
		})

		// Verify that annotations are not re-added and cert is not rotated
		err = wait.PollImmediate(10*time.Second, 2*time.Minute, func() (bool, error) {
			s, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}

			ann := s.Annotations
			_, hasComponent := ann["openshift.io/owning-component"]
			_, hasDescription := ann["openshift.io/description"]
			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])

			// If annotations are present or cert changed, fail
			if hasComponent || hasDescription || dataNow != dataBefore {
				return false, fmt.Errorf("unexpected change: hasComponent=%v, hasDescription=%v, certChanged=%v",
					hasComponent, hasDescription, dataNow != dataBefore)
			}

			return true, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "annotations should not be re-added and cert should not rotate when operator is down")
	})

	g.It("should restore metadata when operator is up without cert rotation", func() {
		g.By("Scenario B: operator up, metadata restored without cert rotation")

		// Ensure operator is running first
		scaleDeployment(kubeClient, ctx, operatorNamespace, operatorName, 1)

		// Wait a bit for operator to be ready
		time.Sleep(10 * time.Second)

		// Get the current secret and save the cert data
		sec, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		dataBefore := base64.StdEncoding.EncodeToString(sec.Data["tls.crt"])

		// Remove metadata annotations to test restoration
		g.By("Removing metadata annotations")
		patchAnnotationsOnSecret(kubeClient, ctx, targetNamespace, secretName, map[string]*string{
			"openshift.io/owning-component": nil,
			"openshift.io/description":      nil,
		})

		// Wait for metadata to be restored without cert rotation
		g.By("Waiting for operator to restore metadata")
		err = wait.PollImmediate(5*time.Second, 1*time.Minute, func() (bool, error) {
			s, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}

			ann := s.Annotations
			hasComponent := ann["openshift.io/owning-component"] != ""
			hasDescription := ann["openshift.io/description"] != ""
			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])

			if hasComponent && hasDescription && dataNow == dataBefore {
				return true, nil
			}

			g.GinkgoLogr.Info("Waiting for metadata restoration",
				"hasComponent", hasComponent,
				"hasDescription", hasDescription,
				"certChanged", dataNow != dataBefore)
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "metadata should be restored without cert rotation when operator is up")
	})

	g.It("should rotate cert when expired and operator is down", func() {
		g.By("Scenario C: operator down, expired certificate-not-after triggers sidecar rotation")

		// Get the current secret and save the cert data
		sec, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		dataBefore := base64.StdEncoding.EncodeToString(sec.Data["tls.crt"])

		// Scale down the operator
		scaleDeployment(kubeClient, ctx, operatorNamespace, operatorName, 0)

		// Set certificate-not-after to a past date to trigger rotation by sidecar
		// Note: The sidecar checks auth.openshift.io/certificate-not-after, not auth.openshift.io/not-after
		past := "2000-01-01T00:00:00Z"
		patchAnnotationsOnSecret(kubeClient, ctx, targetNamespace, secretName, map[string]*string{
			"auth.openshift.io/certificate-not-after": &past,
		})

		// Wait for cert rotation by sidecar
		// The sidecar runs in kube-apiserver pods and should detect the expired cert and rotate it
		g.By("Waiting for sidecar to rotate expired certificate")
		err = wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
			s, err := kubeClient.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}

			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])
			ann := s.Annotations
			certNotAfter := ann["auth.openshift.io/certificate-not-after"]

			if certNotAfter == past {
				g.GinkgoLogr.Info("Waiting for cert rotation", "certNotAfter", certNotAfter)
				return false, nil
			}

			if dataNow != dataBefore {
				g.GinkgoLogr.Info("Certificate rotated", "certNotAfter", certNotAfter)
				return true, nil
			}

			g.GinkgoLogr.Info("Checking cert rotation status",
				"certChanged", dataNow != dataBefore,
				"certNotAfter", certNotAfter)
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "cert should be rotated by sidecar when expired")
	})
})

// pauseCVO pauses the Cluster Version Operator reconciliation.
func pauseCVO(kubeClient *kubernetes.Clientset, configClient *configclient.ConfigV1Client, ctx context.Context) {
	scaleDeployment(kubeClient, ctx, cvoNamespace, cvoName, 0)

	overridePatch := []byte(`{"spec":{"overrides":[{"kind":"Deployment","group":"apps","namespace":"openshift-cluster-version","name":"cluster-version-operator","unmanaged":true}]}}`)
	_, err := configClient.ClusterVersions().Patch(ctx, "version", types.MergePatchType, overridePatch, metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
}

// resumeCVO resumes the Cluster Version Operator reconciliation.
func resumeCVO(kubeClient *kubernetes.Clientset, configClient *configclient.ConfigV1Client, ctx context.Context) {
	scaleDeployment(kubeClient, ctx, cvoNamespace, cvoName, 1)

	overridePatch := []byte(`{"spec":{"overrides":[]}}`)
	_, err := configClient.ClusterVersions().Patch(ctx, "version", types.MergePatchType, overridePatch, metav1.PatchOptions{})
	if err != nil {
		g.GinkgoLogr.Info("Warning: failed to resume CVO", "error", err)
	}
}

// scaleDeployment scales a deployment to the specified number of replicas.
func scaleDeployment(kubeClient *kubernetes.Clientset, ctx context.Context, namespace, name string, replicas int32) {
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	_, err := kubeClient.AppsV1().Deployments(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	// Wait for deployment to scale
	err = wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		deployment, err := kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return isDeploymentScaled(deployment, replicas), nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "deployment %s/%s failed to scale to %d replicas", namespace, name, replicas)
}

// isDeploymentScaled checks if a deployment has been scaled to the expected number of replicas.
func isDeploymentScaled(deployment *appsv1.Deployment, expectedReplicas int32) bool {
	if deployment.Spec.Replicas == nil {
		return false
	}
	if *deployment.Spec.Replicas != expectedReplicas {
		return false
	}
	if deployment.Status.ReadyReplicas != expectedReplicas {
		return false
	}
	return true
}

// patchAnnotationsOnSecret patches annotations on a Secret.
// Pass nil values in the map to remove annotations.
func patchAnnotationsOnSecret(kubeClient *kubernetes.Clientset, ctx context.Context, namespace, name string, annotations map[string]*string) {
	patch := `{"metadata":{"annotations":{`
	first := true

	for k, v := range annotations {
		if !first {
			patch += ","
		}
		if v == nil {
			patch += fmt.Sprintf("%q:null", k)
		} else {
			patch += fmt.Sprintf("%q:%q", k, *v)
		}
		first = false
	}

	patch += "}}}"

	_, err := kubeClient.CoreV1().Secrets(namespace).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
}
