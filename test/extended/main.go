package extended

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	exutil "github.com/openshift/origin/test/extended/util"
)

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery] sanity test", func() {
	g.It("should always pass [Suite:openshift/cluster-kube-apiserver-operator/conformance/parallel]", func() {
		o.Expect(true).To(o.BeTrue())
	})
})

// Helper: scale a Deployment
func scaleDeployment(oc *exutil.CLI, ns, name string, replicas int32) {
	_, err := oc.AdminKubeClient().AppsV1().Deployments(ns).
		Patch(context.Background(), name,
			metav1.TypeMergePatchType,
			[]byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)),
			metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
}

// Helper: patch annotations on a Secret/ConfigMap
func patchAnnotationsOnSecret(oc *exutil.CLI, ns, name string, anns map[string]*string) {
	patch := `{"metadata":{"annotations":{`
	first := true
	for k, v := range anns {
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
	_, err := oc.AdminKubeClient().CoreV1().Secrets(ns).
		Patch(context.Background(), name,
			metav1.TypeMergePatchType, []byte(patch), metav1.PatchOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
}

var _ = g.Describe("[sig-api-machinery][cert-rotation][sidecar-refresh-only-when-expired] merged scenarios", func() {
	defer g.GinkgoRecover()
	oc := exutil.NewCLI("cert-rotation").AsAdmin()

	ns := "openshift-kube-apiserver"
	secretName := "kubelet-client"

	var kc *kubernetes.Clientset
	g.BeforeEach(func() {
		kc = oc.AdminKubeClient()
	})

	g.It("pauses CVO, tests sidecar/operator behavior, then resumes CVO", func() {
		ctx := context.Background()

		// --- Pause CVO
		g.By("Pausing CVO reconciliation")
		scaleDeployment(oc, "openshift-cluster-version", "cluster-version-operator", 0)
		_, err := oc.AdminConfigClient().ConfigV1().ClusterVersions().
			Patch(ctx, "version", metav1.TypeMergePatchType,
				[]byte(`{"spec":{"overrides":[{"kind":"Deployment","group":"apps","namespace":"openshift-cluster-version","name":"cluster-version-operator","unmanaged":true}]}}`),
				metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		defer func() {
			// --- Resume CVO
			g.By("Resuming CVO reconciliation")
			scaleDeployment(oc, "openshift-cluster-version", "cluster-version-operator", 1)
			_, _ = oc.AdminConfigClient().ConfigV1().ClusterVersions().
				Patch(ctx, "version", metav1.TypeMergePatchType,
					[]byte(`{"spec":{"overrides":[]}}`), metav1.PatchOptions{})
		}()

		// --- Scenario A
		g.By("Scenario A: operator down, removing metadata should not be re-added by sidecar")
		scaleDeployment(oc, "openshift-kube-apiserver-operator", "openshift-kube-apiserver-operator", 0)

		sec, err := kc.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		dataBefore := base64.StdEncoding.EncodeToString(sec.Data["tls.crt"])

		patchAnnotationsOnSecret(oc, ns, secretName, map[string]*string{
			"auth.openshift.io/component":   nil,
			"auth.openshift.io/description": nil,
		})

		o.Consistently(func() bool {
			s, _ := kc.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
			ann := s.Annotations
			_, comp := ann["auth.openshift.io/component"]
			_, desc := ann["auth.openshift.io/description"]
			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])
			return !comp && !desc && dataNow == dataBefore
		}, 2*time.Minute, 10*time.Second).Should(o.BeTrue())

		// --- Scenario B
		g.By("Scenario B: operator up, metadata restored without cert rotation")
		scaleDeployment(oc, "openshift-kube-apiserver-operator", "openshift-kube-apiserver-operator", 1)

		o.Eventually(func() bool {
			s, _ := kc.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
			ann := s.Annotations
			comp := ann["auth.openshift.io/component"] != ""
			desc := ann["auth.openshift.io/description"] != ""
			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])
			return comp && desc && dataNow == dataBefore
		}, 3*time.Minute, 10*time.Second).Should(o.BeTrue())

		// --- Scenario C
		g.By("Scenario C: operator down, expired not-after triggers sidecar rotation")
		scaleDeployment(oc, "openshift-kube-apiserver-operator", "openshift-kube-apiserver-operator", 0)
		past := "2000-01-01T00:00:00Z"
		patchAnnotationsOnSecret(oc, ns, secretName, map[string]*string{
			"auth.openshift.io/not-after": ptr.To(past),
		})

		o.Eventually(func() bool {
			s, _ := kc.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
			dataNow := base64.StdEncoding.EncodeToString(s.Data["tls.crt"])
			ann := s.Annotations
			na := ann["auth.openshift.io/not-after"]
			if na == "" {
				return false
			}
			// content rotated
			return dataNow != dataBefore
		}, 3*time.Minute, 10*time.Second).Should(o.BeTrue())

		// scale operator back up for cleanup
		scaleDeployment(oc, "openshift-kube-apiserver-operator", "openshift-kube-apiserver-operator", 1)
	})
})
