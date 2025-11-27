package e2e

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	testlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial] TestTokenRequestAndReview", func() {
		testTokenRequestAndReview()
	})
})

// testTokenRequestAndReview checks that bound sa tokens are correctly
// configured. A token is requested via the TokenRequest API and
// validated via the TokenReview API.
func testTokenRequestAndReview() {
	kubeConfig, err := testlibrary.NewClientConfigForTest(g.GinkgoT())
	o.Expect(err).NotTo(o.HaveOccurred())
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())
	corev1client := kubeClient.CoreV1()

	// Create all test resources in a temp namespace that will be
	// removed at the end of the test to avoid requiring explicit
	// cleanup.
	ns, err := corev1client.Namespaces().Create(context.TODO(), &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-token-request-",
		},
	}, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	defer func() {
		err := corev1client.Namespaces().Delete(context.TODO(), ns.Name, metav1.DeleteOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
	}()
	namespace := ns.Name

	sa, err := corev1client.ServiceAccounts(namespace).Create(context.TODO(), &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-service-account",
		},
	}, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	treq, err := corev1client.ServiceAccounts(sa.Namespace).CreateToken(context.TODO(),
		sa.Name,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				// Avoid specifying any audiences so that the token will be
				// issued for the default audience of the issuer.
			},
		},
		metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())

	trev, err := kubeClient.AuthenticationV1().TokenReviews().Create(context.TODO(), &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: treq.Status.Token,
		},
	}, metav1.CreateOptions{})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(trev.Status.Error).To(o.BeEmpty())
	o.Expect(trev.Status.Authenticated).To(o.BeTrue())
}
