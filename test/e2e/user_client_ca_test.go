package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/cert"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

func TestUserClientCABundle(t *testing.T) {

	kubeConfig, err := test.NewClientConfigForTest(t)
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	// create cryptographic materials
	clientCA := test.NewCertificateAuthorityCertificate(t, nil)

	// create ca-bundle ConfigMap
	configMapName := strings.ToLower(test.GenerateNameForTest(t, "UserCA"))
	_, err = kubeClient.ConfigMaps("openshift-config").Create(context.TODO(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: configMapName},
			Data:       map[string]string{"ca-bundle.crt": string(encodeCertPEM(clientCA.Certificate))},
		},
		metav1.CreateOptions{})
	require.NoError(t, err)

	// configure user client-ca
	defer func() {
		_, err := updateAPIServerClusterConfigSpec(configClient, func(apiServer *configv1.APIServer) {
			apiServer.Spec.ClientCA.Name = ""
		})
		assert.NoError(t, err)
	}()
	_, err = updateAPIServerClusterConfigSpec(configClient, func(apiServer *configv1.APIServer) {
		apiServer.Spec.ClientCA.Name = configMapName
	})
	require.NoError(t, err)

	// wait for user client-ca to appear in combined client-ca bundle
	var lastResourceVersion string
	err = wait.Poll(test.WaitPollInterval, test.WaitPollTimeout, func() (bool, error) {
		caBundle, err := kubeClient.ConfigMaps("openshift-kube-apiserver").Get(context.TODO(), "client-ca", metav1.GetOptions{})
		if err != nil || caBundle.ResourceVersion == lastResourceVersion {
			return false, nil
		}

		// get the certs from the combined ca-bundle
		certificates, err := cert.ParseCertsPEM([]byte(caBundle.Data["ca-bundle.crt"]))
		if err != nil {
			return false, err
		}

		// check for user ca certificate
		for _, certificate := range certificates {
			if certificate.SerialNumber.String() == clientCA.Certificate.SerialNumber.String() {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "user client-ca not found in combined client-ca bundle")

}
