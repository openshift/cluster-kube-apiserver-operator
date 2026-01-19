package e2e

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	testlibraryapi "github.com/openshift/library-go/test/library/apiserver"

	g "github.com/onsi/ginkgo/v2"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial][Timeout:40m] testNamedCertificates validates serving certificates for user-defined and system server names", func() {
		testNamedCertificates(g.GinkgoTB())
	})
})

func testNamedCertificates(t testing.TB) {

	// create a root certificate authority crypto materials
	rootCA := test.NewCertificateAuthorityCertificate(t, nil)

	// details of the test certs that will be created, keyed by an string "id"
	testCertInfoById := map[string]*testCertInfo{
		"one":   newTestCertInfo(t, "one", rootCA, "one.test"),
		"two":   newTestCertInfo(t, "two", rootCA, "two.test"),
		"three": newTestCertInfo(t, "three", rootCA, "three.test", "four.test"),
	}

	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	// before starting our test make sure that all apis are on the same revision
	// a previous test might have triggered a new revision and failed
	testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

	// create secrets for named serving certificates
	for _, info := range testCertInfoById {
		_, err := createTLSSecret(kubeClient, "openshift-config", info.secretName, info.crypto.PrivateKey, info.crypto.Certificate)
		require.NoError(t, err)
	}

	defer func() {
		for _, info := range testCertInfoById {
			err := deleteSecret(kubeClient, "openshift-config", info.secretName)
			require.NoError(t, err)
		}
	}()

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

	// get serial number of default serving certificate
	// the default is actually the service-network so that we can easily recognize it in error messages for bad names
	defaultServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "service-network-serving-certkey")
	localhostServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "localhost-serving-cert-certkey")
	serviceServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "service-network-serving-certkey")
	externalLoadBalancerCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "external-loadbalancer-serving-certkey")
	internalLoadBalancerCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "internal-loadbalancer-serving-certkey")
	localhostRecoveryServingCertSerialNumber := serialNumberOfCertificateFromSecretOrFail(t, kubeClient, "openshift-kube-apiserver", "localhost-recovery-serving-certkey")

	t.Logf("default serial: %v", defaultServingCertSerialNumber)
	t.Logf("localhost serial: %v", localhostServingCertSerialNumber)
	t.Logf("service serial: %v", serviceServingCertSerialNumber)
	t.Logf("external lb serial: %v", externalLoadBalancerCertSerialNumber)
	t.Logf("internal lb serial: %v", internalLoadBalancerCertSerialNumber)
	t.Logf("localhost recovery serial: %v", localhostRecoveryServingCertSerialNumber)

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
			name:                 "Service openshift",
			serverName:           "openshift",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service openshift.default",
			serverName:           "openshift.default",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service openshift.default.svc",
			serverName:           "openshift.default.svc",
			expectedSerialNumber: serviceServingCertSerialNumber,
		},
		{
			name:                 "Service openshift.default.svc.cluster.local",
			serverName:           "openshift.default.svc.cluster.local",
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
			name:                 "Localhost localhost-recovery",
			serverName:           "localhost-recovery",
			expectedSerialNumber: localhostRecoveryServingCertSerialNumber,
		},
		{
			name:                 "ExternalLoadBalancerHostname",
			serverName:           getExternalAPIServiceHostNameOrFail(t, configClient),
			expectedSerialNumber: externalLoadBalancerCertSerialNumber,
		},
		{
			name:                 "InternalLoadBalancerHostname",
			serverName:           getInternalAPIServiceHostNameOrFail(t, configClient),
			expectedSerialNumber: internalLoadBalancerCertSerialNumber,
		},
		{
			name:                 "UnknownServerHostname",
			serverName:           "unknown.test",
			expectedSerialNumber: defaultServingCertSerialNumber,
		},
	}

	// wait until a new version has been rolled out with the new configuration
	testlibraryapi.WaitForAPIServerToStabilizeOnTheSameRevision(t, kubeClient.Pods(operatorclient.TargetNamespace))

	// execute test cases
	// For simplicity, we use a single code path for both standard tests and Ginkgo (no subtests).
	// TODO: Add support for subtests in Ginkgo https://issues.redhat.com/browse/CNTRLPLANE-2566
	for _, tc := range testCases {
		t.Logf("Running test case: %s", tc.name)
		err = wait.PollImmediate(time.Second, time.Minute, func() (bool, error) {
			serialNumber, err := getReturnedCertSerialNumber(kubeConfig.Host, tc.serverName)
			require.NoError(t, err)
			return tc.expectedSerialNumber == serialNumber, nil
		})
		require.NoError(t, err, "test case %s failed", tc.name)
	}
}

// getReturnedCertSerialNumber connects to apiserver using a custom ServerName and returns the serial number of
// the certificate that the server presents
func getReturnedCertSerialNumber(host, serverName string) (string, error) {
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
		ServerName:            serverName,
		InsecureSkipVerify:    true,
	}
	hostURL, err := url.Parse(host)
	if err != nil {
		return "", err
	}
	conn, err := tls.Dial("tcp", hostURL.Host, tlsConf)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return serialNumber, nil
}

func deleteSecret(client *clientcorev1.CoreV1Client, namespace, name string) error {
	return client.Secrets(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func encodeCertPEM(c *x509.Certificate) []byte {
	block := pem.Block{
		Type:  cert.CertificateBlockType,
		Bytes: c.Raw,
	}
	return pem.EncodeToMemory(&block)
}

func createTLSSecret(client *clientcorev1.CoreV1Client, namespace, name string, privateKey *rsa.PrivateKey, certificate *x509.Certificate) (*corev1.Secret, error) {
	privateKeyBytes, err := keyutil.MarshalPrivateKeyToPEM(privateKey)
	if err != nil {
		return nil, err
	}
	return client.Secrets(namespace).Create(context.TODO(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSPrivateKeyKey: privateKeyBytes,
				corev1.TLSCertKey:       encodeCertPEM(certificate),
			},
		}, metav1.CreateOptions{})
}

func serialNumberOfCertificateFromSecretOrFail(t testing.TB, client *clientcorev1.CoreV1Client, namespace, name string) string {
	result, err := serialNumberOfCertificateFromSecret(client, namespace, name)
	require.NoError(t, err)
	return result
}

func serialNumberOfCertificateFromSecret(client *clientcorev1.CoreV1Client, namespace, name string) (string, error) {
	secret, err := client.Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
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
	apiServer, err := client.APIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		apiServer, err = client.APIServers().Create(context.TODO(), &configv1.APIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, err
	}
	updateFunc(apiServer)
	return client.APIServers().Update(context.TODO(), apiServer, metav1.UpdateOptions{})
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

func getExternalAPIServiceHostNameOrFail(t testing.TB, client *configclient.ConfigV1Client) string {
	result, err := getExternalAPIServiceHostName(client)
	require.NoError(t, err)
	t.Logf("external api service hostname: %v", result)
	return result
}

func getExternalAPIServiceHostName(client *configclient.ConfigV1Client) (string, error) {
	infrastructure, err := client.Infrastructures().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	apiServerURL, err := url.Parse(infrastructure.Status.APIServerURL)
	if err != nil {
		return "", err
	}
	return strings.Split(apiServerURL.Host, ":")[0], nil
}

func getInternalAPIServiceHostNameOrFail(t testing.TB, client *configclient.ConfigV1Client) string {
	result, err := getInternalAPIServiceHostName(client)
	require.NoError(t, err)
	t.Logf("internal api service hostname: %v", result)
	return result
}

func getInternalAPIServiceHostName(client *configclient.ConfigV1Client) (string, error) {
	infrastructure, err := client.Infrastructures().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	apiServerURL, err := url.Parse(infrastructure.Status.APIServerInternalURL)
	if err != nil {
		return "", err
	}
	return strings.Split(apiServerURL.Host, ":")[0], nil
}

func getKubernetesServiceClusterIPOrFail(t testing.TB, client *clientcorev1.CoreV1Client) string {
	result, err := getKubernetesServiceClusterIP(client)
	require.NoError(t, err)
	return result
}

func getKubernetesServiceClusterIP(client *clientcorev1.CoreV1Client) (string, error) {
	service, err := client.Services("default").Get(context.TODO(), "kubernetes", metav1.GetOptions{})
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

func newTestCertInfo(t testing.TB, id string, signer *test.CryptoMaterials, hosts ...string) *testCertInfo {
	return &testCertInfo{
		secretName: strings.ToLower(test.GenerateNameForTest(t, id+"-")),
		hosts:      hosts,
		crypto:     test.NewServerCertificate(t, signer, hosts...),
	}
}
