package certrotationcontroller

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
)

// TestLocalhostServingRotationWithFakeClock proves the Clock fields added by
// https://github.com/openshift/library-go/pull/2570 drive the LocalhostServing
// cert rotation (mirroring the LocalhostServing controller construction,
// including the operator/target namespace split) without real time passing:
// one fake clock is set on all four components, the clock is advanced past
// 75% of the target's validity, and the next sync rotates.
func TestLocalhostServingRotationWithFakeClock(t *testing.T) {
	fakeClock := clocktesting.NewFakePassiveClock(time.Now().Truncate(time.Second))

	secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	configMapIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	client := kubefake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", fakeClock)

	signer := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-serving-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
			Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via localhost.",
		},
		Validity:        24 * time.Hour,
		Refresh:         12 * time.Hour,
		CertificateName: "kube-apiserver.localhost-serving-signer",
		Lister:          corev1listers.NewSecretLister(secretIndexer),
		Client:          client.CoreV1(),
		EventRecorder:   recorder,
		Clock:           fakeClock,
	}

	caBundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-serving-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
			Description:   "CA for recognizing the kube-apiserver when connecting via localhost.",
		},
		Lister:        corev1listers.NewConfigMapLister(configMapIndexer),
		Client:        client.CoreV1(),
		EventRecorder: recorder,
		Clock:         fakeClock,
	}

	target := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "localhost-serving-cert-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
			Description:   "Serving certificate used by the kube-apiserver to terminate requests via localhost.",
		},
		// Refresh of half the validity makes the refresh window open at 50%;
		// the test advances past 75% so rotation is unambiguous.
		Validity:        time.Hour,
		Refresh:         30 * time.Minute,
		CertificateName: "kube-apiserver.localhost-serving",
		CertCreator: &certrotation.ServingRotation{
			Hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
			Clock:     fakeClock,
		},
		Lister:        corev1listers.NewSecretLister(secretIndexer),
		Client:        client.CoreV1(),
		EventRecorder: recorder,
		Clock:         fakeClock,
	}

	sync := func() (*corev1.Secret, error) {
		ca, _, err := signer.EnsureSigningCertKeyPair(context.TODO())
		if err != nil {
			return nil, err
		}
		caBundleCerts, err := caBundle.EnsureConfigMapCABundle(context.TODO(), ca, operatorclient.OperatorNamespace+"/localhost-serving-signer")
		if err != nil {
			return nil, err
		}
		secret, err := target.EnsureTargetCertKeyPair(context.TODO(), ca, caBundleCerts)
		if err != nil {
			return nil, err
		}
		// mirror the informers feeding the written objects back to the listers
		signerSecret, err := client.CoreV1().Secrets(operatorclient.OperatorNamespace).Get(context.TODO(), "localhost-serving-signer", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		_ = secretIndexer.Add(signerSecret)
		caBundleConfigMap, err := client.CoreV1().ConfigMaps(operatorclient.OperatorNamespace).Get(context.TODO(), "localhost-serving-ca", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		_ = configMapIndexer.Add(caBundleConfigMap)
		_ = secretIndexer.Add(secret)
		return secret, nil
	}

	secret, err := sync()
	if err != nil {
		t.Fatal(err)
	}

	// Advance past 75% of the target's 1h validity.
	fakeClock.SetTime(fakeClock.Now().Add(45 * time.Minute))

	client.ClearActions()
	newSecret, err := sync()
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(newSecret.Data["tls.crt"], secret.Data["tls.crt"]) {
		t.Error("expected the certificate to rotate after advancing the clock")
	}
	secretUpdated := false
	for _, action := range client.Actions() {
		if action.Matches("update", "secrets") {
			secretUpdated = true
		}
	}
	if !secretUpdated {
		t.Errorf("expected secret to get updated, but only found actions: %v", client.Actions())
	}

	// The new certificate must be stamped at the fake clock's time, proving
	// generation (not just the rotation decision) honors the injected clock.
	newCA, err := crypto.GetCAFromBytes(newSecret.Data["tls.crt"], newSecret.Data["tls.key"])
	if err != nil {
		t.Fatal(err)
	}
	wantNotBefore := fakeClock.Now().Add(-time.Second)
	if !newCA.Config.Certs[0].NotBefore.Equal(wantNotBefore) {
		t.Errorf("rotated NotBefore = %v, want %v (generation must use the fake clock)", newCA.Config.Certs[0].NotBefore, wantNotBefore)
	}

	// Steady state: without advancing the clock, another sync must not rotate.
	client.ClearActions()
	steadySecret, err := sync()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(steadySecret.Data["tls.crt"], newSecret.Data["tls.crt"]) {
		t.Error("expected the certificate to be unchanged in steady state")
	}
	for _, action := range client.Actions() {
		if action.Matches("update", "secrets") || action.Matches("create", "secrets") {
			t.Errorf("expected no secret writes in steady state, found: %v", action)
		}
	}
}
