package encryption

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	AuthTargetNamespace   = "openshift-oauth-apiserver"
	AuthOperatorNamespace = "openshift-authentication-operator"
	oauthTokenName        = "sha256~test-oauth-token-of-life"
)

var AuthTargetGRs = []schema.GroupResource{
	{Group: "oauth.openshift.io", Resource: "oauthaccesstokens"},
}

var AuthLabelSelector = "encryption.apiserver.operator.openshift.io/component=" + AuthTargetNamespace
var AuthEncryptionConfigSecretName = fmt.Sprintf("encryption-config-%s", AuthTargetNamespace)

var oauthAccessTokenGVR = schema.GroupVersionResource{
	Group:    "oauth.openshift.io",
	Version:  "v1",
	Resource: "oauthaccesstokens",
}

func getDynamicClient(t testing.TB) dynamic.Interface {
	t.Helper()
	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	dynClient, err := dynamic.NewForConfig(kubeConfig)
	require.NoError(t, err)
	return dynClient
}

func GetRawOAuthTokenOfLife(t testing.TB, clientSet library.ClientSet, _ string) string {
	t.Helper()
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	prefix := fmt.Sprintf("/openshift.io/oauthaccesstokens/%s", oauthTokenName)
	resp, err := clientSet.Etcd.Get(timeout, prefix, clientv3.WithPrefix())
	require.NoError(t, err)
	if len(resp.Kvs) != 1 {
		t.Errorf("Expected to get a single key from etcd for OAuthAccessToken, got %d", len(resp.Kvs))
	}
	return string(resp.Kvs[0].Value)
}

func CreateAndStoreOAuthTokenOfLife(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
	t.Helper()
	dynClient := getDynamicClient(t)
	tokenClient := dynClient.Resource(oauthAccessTokenGVR)

	_, err := tokenClient.Get(context.TODO(), oauthTokenName, metav1.GetOptions{})
	if err == nil {
		t.Log("The OAuthAccessToken already exists, removing it first")
		err := tokenClient.Delete(context.TODO(), oauthTokenName, metav1.DeleteOptions{})
		if err != nil {
			t.Errorf("Failed to delete OAuthAccessToken %s: %v", oauthTokenName, err)
		}
	} else if !errors.IsNotFound(err) {
		t.Errorf("Failed to check if OAuthAccessToken exists: %v", err)
	}

	t.Logf("Creating OAuthAccessToken %q", oauthTokenName)
	token := OAuthTokenOfLife(t, "")
	created, err := tokenClient.Create(context.TODO(), token.(*unstructured.Unstructured), metav1.CreateOptions{})
	require.NoError(t, err)
	return created
}

func OAuthTokenOfLife(t testing.TB, _ string) runtime.Object {
	t.Helper()
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "oauth.openshift.io/v1",
			"kind":       "OAuthAccessToken",
			"metadata": map[string]interface{}{
				"name": oauthTokenName,
			},
			"clientName":               "openshift-browser-client",
			"userName":                 "test-user",
			"userUID":                  "test-uid",
			"redirectURI":              "https://oauth-openshift.example.com/oauth/token/display",
			"scopes":                   []interface{}{"user:full"},
			"expiresIn":                86400,
			"authorizeToken":           "",
			"inactivityTimeoutSeconds": 0,
		},
	}
}

func AssertOAuthTokenOfLifeEncrypted(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
	t.Helper()
	rawValue := GetRawOAuthTokenOfLife(t, clientSet, "")
	if strings.Contains(rawValue, "test-user") {
		t.Errorf("The OAuthAccessToken in etcd contains plain text 'test-user', content: %s", rawValue)
	}
}

func AssertOAuthTokenOfLifeNotEncrypted(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
	t.Helper()
	rawValue := GetRawOAuthTokenOfLife(t, clientSet, "")
	if !strings.Contains(rawValue, "test-user") {
		t.Errorf("The OAuthAccessToken in etcd doesn't have 'test-user', content: %s", rawValue)
	}
}

func AssertOAuthTokens(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertOAuthAccessTokens(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, AuthTargetGRs, namespace, labelSelector)
}

func assertOAuthAccessTokens(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all OAuthAccessTokens were encrypted/decrypted for %q mode", expectedMode)
	total, err := library.VerifyResources(t, etcdClient, "/openshift.io/oauthaccesstokens/", expectedMode, true)
	t.Logf("Verified %d OAuthAccessTokens", total)
	require.NoError(t, err)
}

// Ensure getDynamicClient uses the kubernetes.Interface when needed.
var _ kubernetes.Interface = (*kubernetes.Clientset)(nil)
