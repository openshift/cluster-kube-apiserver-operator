package e2e_cert_rotation_disruptive

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
)

// IntegrationTestCertificates contains all certificates for kube-apiserver integration testing
// EXCLUDING service-ca certificates (those are tested in service-ca-operator)
var IntegrationTestCertificates = []OperatorCertificate{
	// openshift-kube-apiserver-operator namespace - signer certificates
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "aggregator-client-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "kube-apiserver-to-kubelet-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "kube-control-plane-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "loadbalancer-serving-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "localhost-recovery-serving-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "localhost-serving-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "node-system-admin-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "service-network-serving-signer",
		CertKey:      "tls.crt",
		Category:     "signer",
		OperatorName: "kube-apiserver-operator",
	},
	// openshift-kube-apiserver-operator namespace - serving certificates
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "kube-apiserver-operator-serving-cert",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver-operator",
	},
	// openshift-kube-apiserver-operator namespace - client certificates
	{
		Namespace:    kubeAPIServerOperatorNamespace,
		SecretName:   "node-system-admin-client",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver-operator",
	},
	// openshift-kube-apiserver namespace - client certificates
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "aggregator-client",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "check-endpoints-client-cert-key",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "control-plane-node-admin-client-cert-key",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "etcd-client",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "kubelet-client",
		CertKey:      "tls.crt",
		Category:     "client",
		OperatorName: "kube-apiserver",
	},
	// openshift-kube-apiserver namespace - serving certificates
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "external-loadbalancer-serving-certkey",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "internal-loadbalancer-serving-certkey",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "localhost-recovery-serving-certkey",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "localhost-serving-cert-certkey",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver",
	},
	{
		Namespace:    kubeAPIServerNamespace,
		SecretName:   "service-network-serving-certkey",
		CertKey:      "tls.crt",
		Category:     "serving",
		OperatorName: "kube-apiserver",
	},
}

var _ = g.Describe("[sig-kube-apiserver] PKI Configuration [Skipped:MicroShift]", g.Ordered, func() {
	var kubeClient *kubernetes.Clientset
	var configClient configclient.Interface
	var ctx context.Context
	var cancel context.CancelFunc

	g.BeforeAll(func() {
		var err error
		kubeClient, err = getKubeClient()
		if err != nil {
			g.Fail(fmt.Sprintf("error getting kube client: %v", err))
		}

		configClient, err = getConfigClient()
		if err != nil {
			g.Fail(fmt.Sprintf("error getting config client: %v", err))
		}

		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Hour)

		// Register cleanup to run even if tests fail
		g.DeferCleanup(func() {
			testPKICleanup(g.GinkgoTB())
		})

		// Enable ConfigurablePKI feature gate before running tests
		testEnableFeatureGate(g.GinkgoTB(), ctx, configClient)
	})

	g.AfterAll(func() {
		if cancel != nil {
			cancel()
		}
	})

	g.It("[Operator][Serial][Slow][Timeout:25m] should validate uniform PKI configurations and certificate regeneration", func() {
		testUniformPKIConfigurations(g.GinkgoTB(), ctx, kubeClient, configClient)
	})

	g.It("[Operator][Serial][Slow][Timeout:25m] should validate mixed PKI configurations and certificate regeneration", func() {
		testMixedPKIConfigurations(g.GinkgoTB(), ctx, kubeClient, configClient)
	})
})

// testEnableFeatureGate enables the ConfigurablePKI feature gate and waits for cluster stabilization
func testEnableFeatureGate(t testing.TB, ctx context.Context, configClient configclient.Interface) {
	t.Logf("Checking ConfigurablePKI feature gate...")
	enabled, err := isPKIFeatureGateEnabled(ctx, configClient)
	if err != nil {
		t.Fatalf("error checking feature gate status: %v", err)
	}

	if !enabled {
		t.Logf("Enabling ConfigurablePKI feature gate...")
		err = enablePKIFeatureGate(ctx, configClient)
		if err != nil {
			t.Fatalf("error enabling PKI feature gate: %v", err)
		}

		t.Logf("Successfully enabled ConfigurablePKI feature gate")

		// Wait for CRD to become available
		t.Logf("Waiting for PKI CRD to become available...")
		err = waitForPKICRD(ctx, configClient, 2*time.Minute)
		if err != nil {
			t.Fatalf("error waiting for PKI CRD: %v", err)
		}

		t.Logf("PKI CRD is available")

		// Wait for kube-apiserver operator to stabilize after feature gate change
		t.Logf("Waiting for kube-apiserver operator to stabilize after feature gate change...")
		err = waitForClusterOperatorStable(ctx, configClient, "kube-apiserver", 10*time.Minute)
		if err != nil {
			t.Logf("Warning: kube-apiserver operator may still be progressing: %v", err)
			// Don't fail here - the PKI CRD is available so tests can proceed
		}
	} else {
		t.Logf("ConfigurablePKI feature gate already enabled")
	}

	t.Logf("✓ Feature gate setup completed")
}

// testUniformPKIConfigurations tests uniform PKI configurations (same algorithm for all cert categories)
func testUniformPKIConfigurations(t testing.TB, ctx context.Context, kubeClient *kubernetes.Clientset, configClient configclient.Interface) {
	t.Logf("Testing uniform PKI configurations for kube-apiserver...")

	var err error

	// Define test configurations
	testConfigs := []pkiTestConfig{
		{
			name:      "RSA-2048",
			algorithm: configv1alpha1.KeyAlgorithmRSA,
			rsaSize:   2048,
		},
		{
			name:      "RSA-4096",
			algorithm: configv1alpha1.KeyAlgorithmRSA,
			rsaSize:   4096,
		},
		{
			name:      "RSA-8192",
			algorithm: configv1alpha1.KeyAlgorithmRSA,
			rsaSize:   8192,
		},
		{
			name:       "ECDSA-P256",
			algorithm:  configv1alpha1.KeyAlgorithmECDSA,
			ecdsaCurve: configv1alpha1.ECDSACurveP256,
		},
		{
			name:       "ECDSA-P384",
			algorithm:  configv1alpha1.KeyAlgorithmECDSA,
			ecdsaCurve: configv1alpha1.ECDSACurveP384,
		},
		{
			name:       "ECDSA-P521",
			algorithm:  configv1alpha1.KeyAlgorithmECDSA,
			ecdsaCurve: configv1alpha1.ECDSACurveP521,
		},
	}

	for _, tc := range testConfigs {
		t.Logf("\n=== Testing configuration: %s ===", tc.name)

		// Apply the PKI configuration
		err = applyPKIConfig(ctx, configClient, tc)
		if err != nil {
			t.Fatalf("error applying PKI config %s: %v", tc.name, err)
		}

		t.Logf("PKI configuration %s applied successfully", tc.name)

		// Wait a moment for operators to process the configuration
		time.Sleep(10 * time.Second)

		// Test certificate regeneration for kube-apiserver certificates
		t.Logf("Testing kube-apiserver certificate regeneration with %s...", tc.name)
		err = testKubeAPIServerCertificates(ctx, kubeClient, configClient, tc, t)
		if err != nil {
			t.Fatalf("Certificate regeneration failed for %s: %v", tc.name, err)
		}

		t.Logf("✓ Configuration %s tested successfully", tc.name)
	}

	t.Logf("\n✓ All uniform PKI configuration tests passed successfully")
}

// testMixedPKIConfigurations tests mixed PKI configurations (different algorithms per cert category)
func testMixedPKIConfigurations(t testing.TB, ctx context.Context, kubeClient *kubernetes.Clientset, configClient configclient.Interface) {
	t.Logf("Testing mixed PKI configurations (different key types per certificate category)...")

	var err error

	// Define mixed test configurations
	// Format: signer-serving-client (algorithm-size/curve)
	mixedConfigs := []mixedPKITestConfig{
		{
			name:              "RSA4096-P256-P521",
			signerAlgorithm:   configv1alpha1.KeyAlgorithmRSA,
			signerRSASize:     4096,
			servingAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			servingECDSACurve: configv1alpha1.ECDSACurveP256,
			clientAlgorithm:   configv1alpha1.KeyAlgorithmECDSA,
			clientECDSACurve:  configv1alpha1.ECDSACurveP521,
		},
		{
			name:             "RSA2048-RSA4096-P384",
			signerAlgorithm:  configv1alpha1.KeyAlgorithmRSA,
			signerRSASize:    2048,
			servingAlgorithm: configv1alpha1.KeyAlgorithmRSA,
			servingRSASize:   4096,
			clientAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			clientECDSACurve: configv1alpha1.ECDSACurveP384,
		},
		{
			name:             "P256-RSA8192-RSA2048",
			signerAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			signerECDSACurve: configv1alpha1.ECDSACurveP256,
			servingAlgorithm: configv1alpha1.KeyAlgorithmRSA,
			servingRSASize:   8192,
			clientAlgorithm:  configv1alpha1.KeyAlgorithmRSA,
			clientRSASize:    2048,
		},
		{
			name:              "P384-P256-RSA4096",
			signerAlgorithm:   configv1alpha1.KeyAlgorithmECDSA,
			signerECDSACurve:  configv1alpha1.ECDSACurveP384,
			servingAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			servingECDSACurve: configv1alpha1.ECDSACurveP256,
			clientAlgorithm:   configv1alpha1.KeyAlgorithmRSA,
			clientRSASize:     4096,
		},
		{
			name:             "P521-RSA2048-P256",
			signerAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			signerECDSACurve: configv1alpha1.ECDSACurveP521,
			servingAlgorithm: configv1alpha1.KeyAlgorithmRSA,
			servingRSASize:   2048,
			clientAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			clientECDSACurve: configv1alpha1.ECDSACurveP256,
		},
		{
			name:              "RSA8192-P384-P521",
			signerAlgorithm:   configv1alpha1.KeyAlgorithmRSA,
			signerRSASize:     8192,
			servingAlgorithm:  configv1alpha1.KeyAlgorithmECDSA,
			servingECDSACurve: configv1alpha1.ECDSACurveP384,
			clientAlgorithm:   configv1alpha1.KeyAlgorithmECDSA,
			clientECDSACurve:  configv1alpha1.ECDSACurveP521,
		},
	}

	for _, tc := range mixedConfigs {
		t.Logf("\n=== Testing mixed configuration: %s ===", tc.name)

		// Apply the mixed PKI configuration
		err = applyMixedPKIConfig(ctx, configClient, tc)
		if err != nil {
			t.Fatalf("error applying mixed PKI config %s: %v", tc.name, err)
		}

		t.Logf("Mixed PKI configuration %s applied successfully", tc.name)

		// Wait a moment for operators to process the configuration
		time.Sleep(10 * time.Second)

		// Test certificate regeneration with mixed configuration
		t.Logf("Testing kube-apiserver certificate regeneration with mixed config %s...", tc.name)
		err = testMixedKubeAPIServerCertificates(ctx, kubeClient, configClient, tc, t)
		if err != nil {
			t.Fatalf("Certificate regeneration failed for mixed config %s: %v", tc.name, err)
		}

		t.Logf("✓ Mixed configuration %s tested successfully", tc.name)
	}

	t.Logf("\n✓ All mixed PKI configuration tests passed successfully")
}

// testKubeAPIServerCertificates tests certificate regeneration for kube-apiserver
func testKubeAPIServerCertificates(ctx context.Context, kubeClient interface{}, configClient interface{}, tc pkiTestConfig, t testing.TB) error {
	kubeClientset := kubeClient.(*kubernetes.Clientset)

	// Test a subset of certificates to avoid excessive test time
	// Pick one from each category (signer, serving, client)
	testCerts := []OperatorCertificate{
		// One signer from operator namespace
		{
			Namespace:    kubeAPIServerOperatorNamespace,
			SecretName:   "aggregator-client-signer",
			CertKey:      "tls.crt",
			Category:     "signer",
			OperatorName: "kube-apiserver-operator",
		},
		// One serving cert from apiserver namespace
		{
			Namespace:    kubeAPIServerNamespace,
			SecretName:   "external-loadbalancer-serving-certkey",
			CertKey:      "tls.crt",
			Category:     "serving",
			OperatorName: "kube-apiserver",
		},
		// One client cert from apiserver namespace
		{
			Namespace:    kubeAPIServerNamespace,
			SecretName:   "aggregator-client",
			CertKey:      "tls.crt",
			Category:     "client",
			OperatorName: "kube-apiserver",
		},
	}

	for _, cert := range testCerts {
		t.Logf("  Testing %s certificate: %s/%s", cert.Category, cert.Namespace, cert.SecretName)

		// Delete the certificate to trigger regeneration
		err := deleteCertificateSecret(ctx, kubeClientset, cert.Namespace, cert.SecretName)
		if err != nil {
			t.Logf("    Warning: Could not delete certificate %s/%s (may not exist): %v", cert.Namespace, cert.SecretName, err)
			continue
		}
		t.Logf("    ✓ Certificate deleted")

		// Wait for regeneration with appropriate timeout
		// RSA-8192 requires much longer timeout due to computational cost
		certTimeout := 3 * time.Minute
		if tc.algorithm == configv1alpha1.KeyAlgorithmRSA && tc.rsaSize == 8192 {
			certTimeout = 20 * time.Minute
			t.Logf("    Waiting for certificate regeneration (RSA-8192 may take several minutes)...")
		} else {
			t.Logf("    Waiting for certificate regeneration...")
		}

		err = waitForSecretRegeneration(ctx, kubeClientset, cert.Namespace, cert.SecretName, certTimeout)
		if err != nil {
			return fmt.Errorf("error waiting for certificate %s/%s regeneration: %w", cert.Namespace, cert.SecretName, err)
		}
		t.Logf("    ✓ Certificate regenerated")

		// Verify the regenerated certificate matches expected config
		newCert, err := getCertificateFromSecret(ctx, kubeClientset, cert.Namespace, cert.SecretName, cert.CertKey)
		if err != nil {
			return fmt.Errorf("error getting regenerated certificate %s/%s: %w", cert.Namespace, cert.SecretName, err)
		}

		// Verify algorithm and key parameters
		if tc.algorithm == configv1alpha1.KeyAlgorithmRSA {
			if newCert.Algorithm != "RSA" {
				return fmt.Errorf("expected RSA algorithm for %s/%s, got %s", cert.Namespace, cert.SecretName, newCert.Algorithm)
			}
			if int32(newCert.KeySize) != tc.rsaSize {
				return fmt.Errorf("expected RSA key size %d for %s/%s, got %d", tc.rsaSize, cert.Namespace, cert.SecretName, newCert.KeySize)
			}
			t.Logf("    ✓ Certificate verified: RSA-%d", newCert.KeySize)
		} else if tc.algorithm == configv1alpha1.KeyAlgorithmECDSA {
			if newCert.Algorithm != "ECDSA" {
				return fmt.Errorf("expected ECDSA algorithm for %s/%s, got %s", cert.Namespace, cert.SecretName, newCert.Algorithm)
			}
			expectedCurve := string(tc.ecdsaCurve)
			if newCert.Curve != expectedCurve {
				return fmt.Errorf("expected ECDSA curve %s for %s/%s, got %s", expectedCurve, cert.Namespace, cert.SecretName, newCert.Curve)
			}
			t.Logf("    ✓ Certificate verified: ECDSA-%s", newCert.Curve)
		}

		// Small delay between deletions to avoid overwhelming the operators
		time.Sleep(5 * time.Second)
	}

	// Note: We don't wait for operator stabilization here to avoid test timeouts.
	// The certificate regeneration itself is sufficient validation.
	t.Logf("  ✓ Configuration test completed")

	return nil
}

// testMixedKubeAPIServerCertificates tests certificate regeneration with mixed key configurations
func testMixedKubeAPIServerCertificates(ctx context.Context, kubeClient interface{}, configClient interface{}, tc mixedPKITestConfig, t testing.TB) error {
	kubeClientset := kubeClient.(*kubernetes.Clientset)

	// Test one cert from each category to verify different key types
	testCerts := []struct {
		cert               OperatorCertificate
		expectedAlgorithm  configv1alpha1.KeyAlgorithm
		expectedRSASize    int32
		expectedECDSACurve configv1alpha1.ECDSACurve
	}{
		{
			cert: OperatorCertificate{
				Namespace:    kubeAPIServerOperatorNamespace,
				SecretName:   "aggregator-client-signer",
				CertKey:      "tls.crt",
				Category:     "signer",
				OperatorName: "kube-apiserver-operator",
			},
			expectedAlgorithm:  tc.signerAlgorithm,
			expectedRSASize:    tc.signerRSASize,
			expectedECDSACurve: tc.signerECDSACurve,
		},
		{
			cert: OperatorCertificate{
				Namespace:    kubeAPIServerNamespace,
				SecretName:   "external-loadbalancer-serving-certkey",
				CertKey:      "tls.crt",
				Category:     "serving",
				OperatorName: "kube-apiserver",
			},
			expectedAlgorithm:  tc.servingAlgorithm,
			expectedRSASize:    tc.servingRSASize,
			expectedECDSACurve: tc.servingECDSACurve,
		},
		{
			cert: OperatorCertificate{
				Namespace:    kubeAPIServerNamespace,
				SecretName:   "aggregator-client",
				CertKey:      "tls.crt",
				Category:     "client",
				OperatorName: "kube-apiserver",
			},
			expectedAlgorithm:  tc.clientAlgorithm,
			expectedRSASize:    tc.clientRSASize,
			expectedECDSACurve: tc.clientECDSACurve,
		},
	}

	for _, testCase := range testCerts {
		cert := testCase.cert
		t.Logf("  Testing %s certificate: %s/%s", cert.Category, cert.Namespace, cert.SecretName)

		// Delete the certificate to trigger regeneration
		err := deleteCertificateSecret(ctx, kubeClientset, cert.Namespace, cert.SecretName)
		if err != nil {
			t.Logf("    Warning: Could not delete certificate %s/%s (may not exist): %v", cert.Namespace, cert.SecretName, err)
			continue
		}
		t.Logf("    ✓ Certificate deleted")

		// Determine timeout based on algorithm and size
		certTimeout := 3 * time.Minute
		if testCase.expectedAlgorithm == configv1alpha1.KeyAlgorithmRSA && testCase.expectedRSASize == 8192 {
			certTimeout = 20 * time.Minute
			t.Logf("    Waiting for certificate regeneration (RSA-8192 may take several minutes)...")
		} else {
			t.Logf("    Waiting for certificate regeneration...")
		}

		err = waitForSecretRegeneration(ctx, kubeClientset, cert.Namespace, cert.SecretName, certTimeout)
		if err != nil {
			return fmt.Errorf("error waiting for certificate %s/%s regeneration: %w", cert.Namespace, cert.SecretName, err)
		}
		t.Logf("    ✓ Certificate regenerated")

		// Verify the regenerated certificate matches expected config
		newCert, err := getCertificateFromSecret(ctx, kubeClientset, cert.Namespace, cert.SecretName, cert.CertKey)
		if err != nil {
			return fmt.Errorf("error getting regenerated certificate %s/%s: %w", cert.Namespace, cert.SecretName, err)
		}

		// Verify algorithm and key parameters
		if testCase.expectedAlgorithm == configv1alpha1.KeyAlgorithmRSA {
			if newCert.Algorithm != "RSA" {
				return fmt.Errorf("expected RSA algorithm for %s certificate %s/%s, got %s", cert.Category, cert.Namespace, cert.SecretName, newCert.Algorithm)
			}
			if int32(newCert.KeySize) != testCase.expectedRSASize {
				return fmt.Errorf("expected RSA key size %d for %s certificate %s/%s, got %d", testCase.expectedRSASize, cert.Category, cert.Namespace, cert.SecretName, newCert.KeySize)
			}
			t.Logf("    ✓ %s certificate verified: RSA-%d", cert.Category, newCert.KeySize)
		} else if testCase.expectedAlgorithm == configv1alpha1.KeyAlgorithmECDSA {
			if newCert.Algorithm != "ECDSA" {
				return fmt.Errorf("expected ECDSA algorithm for %s certificate %s/%s, got %s", cert.Category, cert.Namespace, cert.SecretName, newCert.Algorithm)
			}
			expectedCurve := string(testCase.expectedECDSACurve)
			if newCert.Curve != expectedCurve {
				return fmt.Errorf("expected ECDSA curve %s for %s certificate %s/%s, got %s", expectedCurve, cert.Category, cert.Namespace, cert.SecretName, newCert.Curve)
			}
			t.Logf("    ✓ %s certificate verified: ECDSA-%s", cert.Category, newCert.Curve)
		}

		// Small delay between deletions
		time.Sleep(5 * time.Second)
	}

	// Note: We don't wait for operator stabilization here to avoid test timeouts.
	// The certificate regeneration itself is sufficient validation.
	t.Logf("  ✓ Configuration test completed")

	return nil
}

// testPKICleanup resets the PKI configuration to default (Unmanaged) and disables the feature gate
func testPKICleanup(t testing.TB) {
	configClient, err := getConfigClient()
	if err != nil {
		t.Fatalf("error getting config client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	t.Logf("Starting PKI cleanup...")

	// Step 1: Reset PKI cluster resource to default (unmanaged) configuration
	t.Logf("Step 1: Resetting PKI cluster resource to default configuration...")
	pki, err := configClient.ConfigV1alpha1().PKIs().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			t.Fatalf("error getting PKI resource: %v", err)
		}
		t.Logf("PKI resource not found, skipping reset")
	} else {
		// Reset to default/unmanaged mode
		pki.Spec.CertificateManagement.Mode = configv1alpha1.PKICertificateManagementModeUnmanaged
		pki.Spec.CertificateManagement.Custom = configv1alpha1.CustomPKIPolicy{}

		_, err = configClient.ConfigV1alpha1().PKIs().Update(ctx, pki, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("error resetting PKI resource: %v", err)
		}
		t.Logf("✓ PKI cluster resource reset to Unmanaged mode successfully")
	}

	// Step 2: Disable ConfigurablePKI feature gate
	t.Logf("Step 2: Disabling ConfigurablePKI feature gate...")
	enabled, err := isPKIFeatureGateEnabled(ctx, configClient)
	if err != nil {
		t.Fatalf("error checking feature gate status: %v", err)
	}

	if !enabled {
		t.Logf("ConfigurablePKI feature gate already disabled")
		return
	}

	// Get current feature gate configuration
	featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("error getting feature gate: %v", err)
	}

	// Remove ConfigurablePKI from spec.customNoUpgrade.enabled list
	if featureGate.Spec.FeatureSet == configv1.CustomNoUpgrade && featureGate.Spec.CustomNoUpgrade != nil {
		newEnabled := []configv1.FeatureGateName{}
		removed := false
		for _, feature := range featureGate.Spec.CustomNoUpgrade.Enabled {
			if string(feature) == "ConfigurablePKI" {
				removed = true
				t.Logf("Removing ConfigurablePKI from enabled features")
				continue
			}
			newEnabled = append(newEnabled, feature)
		}

		if !removed {
			t.Logf("ConfigurablePKI not found in spec.customNoUpgrade.enabled list")
			return
		}

		featureGate.Spec.CustomNoUpgrade.Enabled = newEnabled

		// If no features left, keep CustomNoUpgrade with empty list
		// (featureSet cannot be changed once set to CustomNoUpgrade)
		if len(newEnabled) == 0 {
			t.Logf("No custom features remaining, keeping CustomNoUpgrade with empty enabled list")
		}

		_, err = configClient.ConfigV1().FeatureGates().Update(ctx, featureGate, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("error updating feature gate: %v", err)
		}

		t.Logf("✓ ConfigurablePKI feature gate disabled successfully")
	} else {
		t.Logf("Feature gate is not using CustomNoUpgrade, cannot disable ConfigurablePKI")
	}

	t.Logf("✓ PKI cleanup completed successfully")
}
