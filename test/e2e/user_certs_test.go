package e2e

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/cert"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestNamedCertificates(t *testing.T) {

	// create a root certificate authority crypto materials
	rootCA := test.NewCertificateAuthorityCertificate(t, nil)

	// details of the test certs that will be created, keyed by an string "id"
	testCertInfoById := map[string]*testCertInfo{
		"one":   newTestCertInfo(t, "one", rootCA.Certificate, "one.test"),
		"two":   newTestCertInfo(t, "two", rootCA.Certificate, "two.test"),
		"three": newTestCertInfo(t, "three", rootCA.Certificate, "three.test", "four.test"),
	}

	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	// kube-apiserver must be available, not progressing, and not failing to continue
	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotFailing(t, configClient)

	// create secrets for named serving certificates
	for _, info := range testCertInfoById {
		defer func(info *testCertInfo) {
			err := deleteSecret(kubeClient, "openshift-config", info.secretName)
			require.NoError(t, err)
		}(info)
		_, err := createTLSSecret(kubeClient, "openshift-config", info.secretName, info.crypto.PrivateKey, info.crypto.Certificate)
		require.NoError(t, err)
	}

	// before updating the config, note current generation of KubeApiServer/cluster.
	initialConfigGeneration := test.GetKubeAPIServerOperatorConfigGeneration(t, operatorClient)

	// configure named certificates
	defer func() {
		_, err := updateAPIServerClusterConfigSpec(configClient, func(apiserver *configv1.APIServer) {
			removeNamedCertificatesBySecretName(apiserver,
				testCertInfoById["one"].secretName,
				testCertInfoById["two"].secretName,
				testCertInfoById["three"].secretName,
			)
		})
		assert.NoError(t, err)
	}()
	_, err = updateAPIServerClusterConfigSpec(configClient, func(apiServer *configv1.APIServer) {
		apiServer.Spec.ServingCerts.NamedCertificates = append(
			apiServer.Spec.ServingCerts.NamedCertificates,
			configv1.APIServerNamedServingCert{ServingCertificate: configv1.SecretNameReference{Name: testCertInfoById["one"].secretName}},
			configv1.APIServerNamedServingCert{ServingCertificate: configv1.SecretNameReference{Name: testCertInfoById["two"].secretName}},
			configv1.APIServerNamedServingCert{ServingCertificate: configv1.SecretNameReference{Name: testCertInfoById["three"].secretName}},
		)
	})
	require.NoError(t, err)

	// wait for configuration to become effective
	test.WaitForNextKubeAPIServerOperatorConfigGenerationToFinishProgressing(t, operatorClient, initialConfigGeneration)
	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotFailing(t, configClient)

	// get serial number of default serving certificate
	defaultServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "serving-cert")
	localhostServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "localhost-serving-cert-certkey")
	serviceServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "service-network-serving-certkey")
	externalLoadBalancerCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "external-loadbalancer-serving-certkey")

	// execute test cases
	testCases := []struct {
		name                 string
		serverName           string
		expectedSerialNumber string
	}{
		{
			name:                 "User one.test",
			serverName:           "one.test",
			expectedSerialNumber: testCertInfoById["one"].crypto.Certificate.SerialNumber.String(),
		},
		{
			name:                 "User two.test",
			serverName:           "two.test",
			expectedSerialNumber: testCertInfoById["two"].crypto.Certificate.SerialNumber.String(),
		},
		{
			name:                 "User three.test",
			serverName:           "three.test",
			expectedSerialNumber: testCertInfoById["three"].crypto.Certificate.SerialNumber.String(),
		},
		{
			name:                 "User four.test",
			serverName:           "four.test",
			expectedSerialNumber: testCertInfoById["three"].crypto.Certificate.SerialNumber.String(),
		},
		{
			name:                 "Service kubernetes",
			serverName:           "kubernetes",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service kubernetes.default",
			serverName:           "kubernetes.default",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service kubernetes.default.svc",
			serverName:           "kubernetes.default.svc",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service kubernetes.default.svc.cluster.local",
			serverName:           "kubernetes.default.svc.cluster.local",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "ServiceIP",
			serverName:           getKubernetesServiceClusterIPOrFail(t, kubeClient),
			expectedSerialNumber: defaultServingCertSerialNumber,
		},
		{
			name:                 "Localhost localhost",
			serverName:           "localhost",
			expectedSerialNumber: localhostServingCertSerialNumber,
		},
		{
			name:                 "Localhost 127.0.0.1",
			serverName:           "127.0.0.1",
			expectedSerialNumber: defaultServingCertSerialNumber,
		},
		{
			name:                 "LoadBalancerHostname",
			serverName:           getAPIServiceHostNameOrFail(t, configClient),
			expectedSerialNumber: externalLoadBalancerCertSerialNumber,
		},
		{
			name:                 "UnknownServerHostname",
			serverName:           "unknown.test",
			expectedSerialNumber: defaultServingCertSerialNumber,
		},
	}

	// execute test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			// connect to apiserver using a custom ServerName and examine the returned certificate's
			// serial number to determine if the expected serving certificate was returned.

			var serialNumber string
			verifyPeerCertificate := func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				var err error
				if certificate, err := x509.ParseCertificate(rawCerts[0]); err == nil {
					serialNumber = certificate.SerialNumber.String()
				}
				return err
			}
			tlsConf := &tls.Config{
				VerifyPeerCertificate: verifyPeerCertificate,
				ServerName:            tc.serverName,
				InsecureSkipVerify:    true,
			}
			hostURL, err := url.Parse(kubeConfig.Host)
			require.NoError(t, err)
			conn, err := tls.Dial("tcp", hostURL.Host, tlsConf)
			defer conn.Close()
			require.NoError(t, err)
			assert.Equal(t, tc.expectedSerialNumber, serialNumber, "Retrieved certificate serial number")
		})
	}

}

func deleteSecret(client *clientcorev1.CoreV1Client, namespace, name string) error {
	return client.Secrets(namespace).Delete(name, &metav1.DeleteOptions{})
}
func createTLSSecret(client *clientcorev1.CoreV1Client, namespace, name string, privateKey *rsa.PrivateKey, certificate *x509.Certificate) (*corev1.Secret, error) {
	return client.Secrets(namespace).Create(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSPrivateKeyKey: cert.EncodePrivateKeyPEM(privateKey),
				corev1.TLSCertKey:       cert.EncodeCertPEM(certificate),
			},
		})
}

func serialNumberOfCertificateFromSecretOrFail(t *testing.T, client *clientcorev1.CoreV1Client, namespace, name string) string {
	result, err := serialNumberOfCertificateFromSecret(client, namespace, name)
	require.NoError(t, err)
	return result
}

func serialNumberOfCertificateFromSecret(client *clientcorev1.CoreV1Client, namespace, name string) (string, error) {
	secret, err := client.Secrets(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	certificates, err := cert.ParseCertsPEM(secret.Data["tls.crt"])
	if err != nil {
		return "", err
	}
	return certificates[0].SerialNumber.String(), nil
}

func updateAPIServerClusterConfigSpec(client *configclient.ConfigV1Client, updateFunc func(spec *configv1.APIServer)) (*configv1.APIServer, error) {
	apiServer, err := client.APIServers().Get("cluster", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		apiServer, err = client.APIServers().Create(&configv1.APIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}})
	}
	if err != nil {
		return nil, err
	}
	updateFunc(apiServer)
	return client.APIServers().Update(apiServer)
}

func removeNamedCertificatesBySecretName(apiServer *configv1.APIServer, secretName ...string) {
	var result []configv1.APIServerNamedServingCert
	for _, namedCertificate := range apiServer.Spec.ServingCerts.NamedCertificates {
		keep := true
		for _, name := range secretName {
			if namedCertificate.ServingCertificate.Name == name {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, namedCertificate)
		}
	}
	apiServer.Spec.ServingCerts.NamedCertificates = result
}

func getAPIServiceHostNameOrFail(t *testing.T, client *configclient.ConfigV1Client) string {
	result, err := getAPIServiceHostName(client)
	require.NoError(t, err)
	return result
}

func getAPIServiceHostName(client *configclient.ConfigV1Client) (string, error) {
	infrastructure, err := client.Infrastructures().Get("cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	apiServerURL, err := url.Parse(infrastructure.Status.APIServerURL)
	if err != nil {
		return "", err
	}
	return strings.Split(apiServerURL.Host, ":")[0], nil
}

func getKubernetesServiceClusterIPOrFail(t *testing.T, client *clientcorev1.CoreV1Client) string {
	result, err := getKubernetesServiceClusterIP(client)
	require.NoError(t, err)
	return result
}

func getKubernetesServiceClusterIP(client *clientcorev1.CoreV1Client) (string, error) {
	service, err := client.Services("default").Get("kubernetes", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return service.Spec.ClusterIP, nil
}

type testCertInfo struct {
	// SNI hosts
	hosts []string
	// name of Secret resource
	secretName string
	// tls materials
	crypto *test.CryptoMaterials
}

func newTestCertInfo(t *testing.T, id string, signer *x509.Certificate, hosts ...string) *testCertInfo {
	return &testCertInfo{
		secretName: strings.ToLower(test.GenerateNameForTest(t, id+"-")),
		hosts:      hosts,
		crypto:     test.NewServerCertificate(t, signer, hosts...),
	}
}
