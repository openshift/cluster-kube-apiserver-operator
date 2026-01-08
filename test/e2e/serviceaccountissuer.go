package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	testlibrary "github.com/openshift/library-go/test/library"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	g "github.com/onsi/ginkgo/v2"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

const (
	saInterval       = 1 * time.Second
	saRegularTimeout = 30 * time.Second
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial] serviceaccountissuer lifecycle test", func() {
		g.By("serviceaccountissuer set in authentication config results in apiserver config")
		testServiceAccountIssuerFirstIssuer(g.GinkgoTB())

		g.By("second serviceaccountissuer set in authentication config results in apiserver config with two issuers")
		testServiceAccountIssuerSecondIssuer(g.GinkgoTB())

		g.By("no serviceaccountissuer set in authentication config results in apiserver config with default issuer set")
		testServiceAccountIssuerDefaultIssuer(g.GinkgoTB())
	})
})

func testServiceAccountIssuerFirstIssuer(t testing.TB) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	authConfigClient, err := configv1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	setServiceAccountIssuer(t, authConfigClient, "https://first.foo.bar")
	err = pollForOperandIssuer(t, kubeClient, []string{"https://first.foo.bar", "https://kubernetes.default.svc"})
	require.NoError(t, err, "pollForOperandIssuer failed")
}

func testServiceAccountIssuerSecondIssuer(t testing.TB) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	authConfigClient, err := configv1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	setServiceAccountIssuer(t, authConfigClient, "https://second.foo.bar")
	err = pollForOperandIssuer(t, kubeClient, []string{"https://second.foo.bar", "https://first.foo.bar", "https://kubernetes.default.svc"})
	require.NoError(t, err, "pollForOperandIssuer failed")
}

func testServiceAccountIssuerDefaultIssuer(t testing.TB) {
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	authConfigClient, err := configv1.NewForConfig(kubeConfig)
	require.NoError(t, err)

	setServiceAccountIssuer(t, authConfigClient, "")
	err = pollForOperandIssuer(t, kubeClient, []string{"https://kubernetes.default.svc"})
	require.NoError(t, err, "pollForOperandIssuer failed")
}

func pollForOperandIssuer(t testing.TB, client clientcorev1.CoreV1Interface, expectedIssuers []string) error {
	return wait.PollImmediate(saInterval, saRegularTimeout, func() (done bool, err error) {
		configMap, err := client.ConfigMaps(operatorclient.TargetNamespace).Get(context.TODO(), "config", metav1.GetOptions{})
		if err != nil {
			t.Errorf("failed to retrieve apiserver config configmap: %v", err)
			return false, nil
		}
		// key has a .yaml extension but actual format is json
		rawConfig := configMap.Data["config.yaml"]
		if len(rawConfig) == 0 {
			t.Logf("config.yaml is empty in apiserver config configmap")
			return false, nil
		}
		config := map[string]interface{}{}
		if err := json.NewDecoder(bytes.NewBuffer([]byte(rawConfig))).Decode(&config); err != nil {
			t.Errorf("error parsing config, %v", err)
			return false, nil
		}
		issuers, found, err := unstructured.NestedStringSlice(config, "apiServerArguments", "service-account-issuer")
		if !found {
			t.Log("apiServerArguments.service-account-issuer not found in config")
			return false, nil
		}
		if !found || !reflect.DeepEqual(expectedIssuers, issuers) {
			t.Logf("expected service account issuers to be %#v, got %#v", expectedIssuers, issuers)
			return false, nil
		}
		return true, nil
	})
}

func setServiceAccountIssuer(t testing.TB, client configv1.ConfigV1Interface, issuer string) {
	auth, err := client.Authentications().Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)
	auth.Spec.ServiceAccountIssuer = issuer
	_, err = client.Authentications().Update(context.TODO(), auth, metav1.UpdateOptions{})
	require.NoError(t, err)
}
