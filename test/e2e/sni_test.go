package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"

	"github.com/ghodss/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/cert"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestSNI(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	client := corev1.NewForConfigOrDie(kubeConfig)

	// we'll need this later
	hostURL, err := url.Parse(kubeConfig.Host)
	if err != nil {
		t.Fatal(err)
	}

	// let's load the certificate from the secrets, keeping track of the serial numbers
	certificateSerialNumbers := map[string]map[string]string{}
	certificateSecretNames := map[string][]string{
		"openshift-kube-apiserver": {
			"serving-cert",
		},
		"openshift-config": {
			"ingrid",
		},
	}
	for namespace, secretNames := range certificateSecretNames {
		if certificateSerialNumbers[namespace] == nil {
			certificateSerialNumbers[namespace] = map[string]string{}
		}
		for _, secretName := range secretNames {
			secret, err := client.Secrets(namespace).Get(secretName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			certificates, err := cert.ParseCertsPEM(secret.Data["tls.crt"])
			if err != nil {
				t.Fatal(err)
			}
			certificateSerialNumbers[namespace][secretName] = certificates[0].SerialNumber.String()
		}
	}

	testCases := []struct {
		serverName           string
		expectedSerialNumber string
	}{
		{
			serverName:           "kubernetes",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "kubernetes.default",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "kubernetes.default.svc",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "kubernetes.default.service.cluster.local",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "localhost",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "173.30.0.1",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "127.0.0.1",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		{
			serverName:           "unknown",
			expectedSerialNumber: certificateSerialNumbers["openshift-kube-apiserver"]["serving-cert"],
		},
		//{
		//	serverName:           "app.example.com",
		//	expectedSerialNumber: certificateSerialNumbers["openshift-config"]["ingrid"],
		//},
	}

	for _, tc := range testCases {
		t.Run(tc.serverName, func(t *testing.T) {

			// keep track of the incoming certificate's serial number
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

			conn, err := tls.Dial("tcp", hostURL.Host, tlsConf)
			defer conn.Close()
			if err != nil {
				t.Fatal(err)
			}

			if serialNumber != tc.expectedSerialNumber {
				t.Errorf("Expected serial number %v. Got serial number %v instead.", tc.expectedSerialNumber, serialNumber)
			}
		})
	}
	if t.Failed() {
		b, _ := yaml.Marshal(certificateSerialNumbers)
		t.Log("\n" + string(b))
	}

}
