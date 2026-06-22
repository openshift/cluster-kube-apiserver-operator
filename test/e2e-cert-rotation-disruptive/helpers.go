package e2e_cert_rotation_disruptive

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
)

const (
	// kubeAPIServerOperatorNamespace is the namespace where the kube-apiserver-operator runs
	kubeAPIServerOperatorNamespace = "openshift-kube-apiserver-operator"

	// kubeAPIServerNamespace is the namespace where the kube-apiserver runs
	kubeAPIServerNamespace = "openshift-kube-apiserver"

	// pollTimeout is used for quick operations
	pollTimeout = 2 * time.Minute

	// pollInterval is the interval between poll attempts
	pollInterval = 5 * time.Second

	// rotationPollTimeout is used for operations that may take longer due to
	// cluster state changes (rotation, regeneration, etc.)
	rotationPollTimeout = 4 * time.Minute

	// rotationTimeout is the maximum time to wait for certificate rotation
	rotationTimeout = 5 * time.Minute
)

// OperatorCertificate represents a certificate managed by the kube-apiserver operator
type OperatorCertificate struct {
	Namespace    string
	SecretName   string
	CertKey      string // Key in the secret containing the certificate (e.g., "tls.crt")
	Category     string // "signer", "serving", or "client"
	OperatorName string // For metrics verification
}

// certInfo contains parsed certificate information
type certInfo struct {
	Algorithm string
	KeySize   int
	Curve     string
}

// getRestConfig returns a rest.Config for the cluster
func getRestConfig() (*rest.Config, error) {
	confPath := "/tmp/admin.conf"
	if conf := os.Getenv("KUBECONFIG"); conf != "" {
		confPath = conf
	}

	config, err := clientcmd.BuildConfigFromFlags("", confPath)
	if err != nil {
		return nil, fmt.Errorf("error loading config: %w", err)
	}

	return config, nil
}

// getKubeClient returns a Kubernetes client
func getKubeClient() (*kubernetes.Clientset, error) {
	config, err := getRestConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

// getConfigClient returns a config client for PKI tests
func getConfigClient() (configclient.Interface, error) {
	config, err := getRestConfig()
	if err != nil {
		return nil, err
	}

	return configclient.NewForConfig(config)
}

// isPKIFeatureGateEnabled checks if ConfigurablePKI feature gate is enabled
func isPKIFeatureGateEnabled(ctx context.Context, configClient configclient.Interface) (bool, error) {
	featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	// Check if using TechPreview or DevPreview
	if featureGate.Spec.FeatureSet == configv1.TechPreviewNoUpgrade ||
		featureGate.Spec.FeatureSet == configv1.DevPreviewNoUpgrade {
		return true, nil
	}

	// Check custom feature set
	if featureGate.Spec.CustomNoUpgrade != nil {
		for _, enabled := range featureGate.Spec.CustomNoUpgrade.Enabled {
			if string(enabled) == "ConfigurablePKI" {
				return true, nil
			}
		}
	}

	return false, nil
}

// enablePKIFeatureGate enables the ConfigurablePKI feature gate
func enablePKIFeatureGate(ctx context.Context, configClient configclient.Interface) error {
	featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Set FeatureSet to CustomNoUpgrade and add ConfigurablePKI to enabled list
	featureGate.Spec.FeatureSet = configv1.CustomNoUpgrade

	if featureGate.Spec.CustomNoUpgrade == nil {
		featureGate.Spec.CustomNoUpgrade = &configv1.CustomFeatureGates{}
	}

	// Check if ConfigurablePKI is already in the list
	found := false
	for _, enabled := range featureGate.Spec.CustomNoUpgrade.Enabled {
		if string(enabled) == "ConfigurablePKI" {
			found = true
			break
		}
	}

	if !found {
		featureGate.Spec.CustomNoUpgrade.Enabled = append(
			featureGate.Spec.CustomNoUpgrade.Enabled,
			"ConfigurablePKI",
		)
	}

	_, err = configClient.ConfigV1().FeatureGates().Update(ctx, featureGate, metav1.UpdateOptions{})
	return err
}

// waitForPKICRD waits for the PKI CRD to become available
func waitForPKICRD(ctx context.Context, configClient configclient.Interface, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := configClient.ConfigV1alpha1().PKIs().List(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			// CRD not available yet
			return false, nil
		}
		return true, nil
	})
}

// pkiTestConfig defines a PKI test configuration
type pkiTestConfig struct {
	name       string
	algorithm  configv1alpha1.KeyAlgorithm
	rsaSize    int32
	ecdsaCurve configv1alpha1.ECDSACurve
}

// mixedPKITestConfig defines a mixed PKI test configuration with different settings per category
type mixedPKITestConfig struct {
	name              string
	signerAlgorithm   configv1alpha1.KeyAlgorithm
	signerRSASize     int32
	signerECDSACurve  configv1alpha1.ECDSACurve
	servingAlgorithm  configv1alpha1.KeyAlgorithm
	servingRSASize    int32
	servingECDSACurve configv1alpha1.ECDSACurve
	clientAlgorithm   configv1alpha1.KeyAlgorithm
	clientRSASize     int32
	clientECDSACurve  configv1alpha1.ECDSACurve
}

// applyPKIConfig applies a PKI configuration based on the test config
func applyPKIConfig(ctx context.Context, configClient configclient.Interface, tc pkiTestConfig) error {
	keyConfig := configv1alpha1.KeyConfig{
		Algorithm: tc.algorithm,
	}

	if tc.algorithm == configv1alpha1.KeyAlgorithmRSA {
		keyConfig.RSA = configv1alpha1.RSAKeyConfig{
			KeySize: tc.rsaSize,
		}
	} else if tc.algorithm == configv1alpha1.KeyAlgorithmECDSA {
		keyConfig.ECDSA = configv1alpha1.ECDSAKeyConfig{
			Curve: tc.ecdsaCurve,
		}
	}

	pki := &configv1alpha1.PKI{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1alpha1.PKISpec{
			CertificateManagement: configv1alpha1.PKICertificateManagement{
				Mode: configv1alpha1.PKICertificateManagementModeCustom,
				Custom: configv1alpha1.CustomPKIPolicy{
					PKIProfile: configv1alpha1.PKIProfile{
						Defaults: configv1alpha1.DefaultCertificateConfig{
							Key: keyConfig,
						},
					},
				},
			},
		},
	}

	// Try to create or update
	existing, err := configClient.ConfigV1alpha1().PKIs().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		// Create new
		_, err = configClient.ConfigV1alpha1().PKIs().Create(ctx, pki, metav1.CreateOptions{})
		return err
	}

	// Update existing
	existing.Spec = pki.Spec
	_, err = configClient.ConfigV1alpha1().PKIs().Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// applyMixedPKIConfig applies a mixed PKI configuration with different settings per category
func applyMixedPKIConfig(ctx context.Context, configClient configclient.Interface, tc mixedPKITestConfig) error {
	// Build default key config (we'll use signer as default)
	defaultKeyConfig := buildKeyConfig(tc.signerAlgorithm, tc.signerRSASize, tc.signerECDSACurve)

	// Build override configs for serving and client
	servingKeyConfig := buildKeyConfig(tc.servingAlgorithm, tc.servingRSASize, tc.servingECDSACurve)
	clientKeyConfig := buildKeyConfig(tc.clientAlgorithm, tc.clientRSASize, tc.clientECDSACurve)

	pki := &configv1alpha1.PKI{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1alpha1.PKISpec{
			CertificateManagement: configv1alpha1.PKICertificateManagement{
				Mode: configv1alpha1.PKICertificateManagementModeCustom,
				Custom: configv1alpha1.CustomPKIPolicy{
					PKIProfile: configv1alpha1.PKIProfile{
						Defaults: configv1alpha1.DefaultCertificateConfig{
							Key: defaultKeyConfig,
						},
						SignerCertificates: configv1alpha1.CertificateConfig{
							Key: defaultKeyConfig,
						},
						ServingCertificates: configv1alpha1.CertificateConfig{
							Key: servingKeyConfig,
						},
						ClientCertificates: configv1alpha1.CertificateConfig{
							Key: clientKeyConfig,
						},
					},
				},
			},
		},
	}

	// Try to create or update
	existing, err := configClient.ConfigV1alpha1().PKIs().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		// Create new
		_, err = configClient.ConfigV1alpha1().PKIs().Create(ctx, pki, metav1.CreateOptions{})
		return err
	}

	// Update existing
	existing.Spec = pki.Spec
	_, err = configClient.ConfigV1alpha1().PKIs().Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// buildKeyConfig builds a KeyConfig from algorithm and size/curve parameters
func buildKeyConfig(algorithm configv1alpha1.KeyAlgorithm, rsaSize int32, ecdsaCurve configv1alpha1.ECDSACurve) configv1alpha1.KeyConfig {
	keyConfig := configv1alpha1.KeyConfig{
		Algorithm: algorithm,
	}

	if algorithm == configv1alpha1.KeyAlgorithmRSA {
		keyConfig.RSA = configv1alpha1.RSAKeyConfig{
			KeySize: rsaSize,
		}
	} else if algorithm == configv1alpha1.KeyAlgorithmECDSA {
		keyConfig.ECDSA = configv1alpha1.ECDSAKeyConfig{
			Curve: ecdsaCurve,
		}
	}

	return keyConfig
}

// getCertificateFromSecret retrieves and parses a certificate from a secret
func getCertificateFromSecret(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, secretName, certKey string) (*certInfo, error) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	certData, ok := secret.Data[certKey]
	if !ok {
		return nil, fmt.Errorf("certificate key %q not found in secret %s/%s", certKey, namespace, secretName)
	}

	return parseCertificate(certData)
}

// parseCertificate parses PEM-encoded certificate data
func parseCertificate(certPEM []byte) (*certInfo, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	info := &certInfo{}

	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		info.Algorithm = "RSA"
		info.KeySize = pub.N.BitLen()
	case *ecdsa.PublicKey:
		info.Algorithm = "ECDSA"
		switch pub.Curve {
		case elliptic.P256():
			info.Curve = "P256"
		case elliptic.P384():
			info.Curve = "P384"
		case elliptic.P521():
			info.Curve = "P521"
		default:
			info.Curve = "Unknown"
		}
	default:
		return nil, fmt.Errorf("unsupported public key type: %T", pub)
	}

	return info, nil
}

// waitForSecretRegeneration waits for a secret to be recreated
func waitForSecretRegeneration(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, secretName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, nil // Secret doesn't exist yet
		}
		return true, nil
	})
}

// waitForClusterOperatorStable waits for a ClusterOperator to become stable
func waitForClusterOperatorStable(ctx context.Context, configClient configclient.Interface, operatorName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		co, err := configClient.ConfigV1().ClusterOperators().Get(ctx, operatorName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		isAvailable := false
		isProgressing := false
		isDegraded := false

		for _, condition := range co.Status.Conditions {
			switch condition.Type {
			case configv1.OperatorAvailable:
				isAvailable = condition.Status == configv1.ConditionTrue
			case configv1.OperatorProgressing:
				isProgressing = condition.Status == configv1.ConditionTrue
			case configv1.OperatorDegraded:
				isDegraded = condition.Status == configv1.ConditionTrue
			}
		}

		if isDegraded {
			return false, nil
		}

		if isAvailable && !isProgressing {
			return true, nil
		}

		return false, nil
	})
}

// waitForAllClusterOperators waits for all cluster operators to stabilize
func waitForAllClusterOperators(ctx context.Context, configClient configclient.Interface, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 30*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		cos, err := configClient.ConfigV1().ClusterOperators().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil
		}

		unstableOperators := []string{}

		for _, co := range cos.Items {
			isAvailable := false
			isProgressing := false
			isDegraded := false

			for _, c := range co.Status.Conditions {
				switch c.Type {
				case configv1.OperatorAvailable:
					isAvailable = c.Status == configv1.ConditionTrue
				case configv1.OperatorProgressing:
					isProgressing = c.Status == configv1.ConditionTrue
				case configv1.OperatorDegraded:
					isDegraded = c.Status == configv1.ConditionTrue
				}
			}

			if isDegraded || !isAvailable || isProgressing {
				unstableOperators = append(unstableOperators, co.Name)
			}
		}

		if len(unstableOperators) > 0 {
			return false, nil
		}

		return true, nil
	})
}

// deleteCertificateSecret deletes a certificate secret to trigger rotation/regeneration
func deleteCertificateSecret(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, secretName string) error {
	return kubeClient.CoreV1().Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
}
