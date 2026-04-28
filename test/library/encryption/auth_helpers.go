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

	configv1 "github.com/openshift/api/config/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	AuthTargetNamespace   = "openshift-oauth-apiserver"
	AuthOperatorNamespace = "openshift-authentication-operator"
	oauthTokenName        = "sha256~token-aaaaaaaa-of-aaaaaaaa-life-aaaaaaaa"
)

var AuthTargetGRs = []schema.GroupResource{
	{Group: "oauth.openshift.io", Resource: "oauthaccesstokens"},
	{Group: "oauth.openshift.io", Resource: "oauthauthorizetokens"},
}

var AuthLabelSelector = "encryption.apiserver.operator.openshift.io/component=" + AuthTargetNamespace
var AuthEncryptionConfigSecretName = fmt.Sprintf("encryption-config-%s", AuthTargetNamespace)

var oauthAccessTokenGVR = schema.GroupVersionResource{
	Group:    "oauth.openshift.io",
	Version:  "v1",
	Resource: "oauthaccesstokens",
}

func getAuthDynamicClient(t testing.TB) dynamic.Interface {
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

	prefix := fmt.Sprintf("/openshift.io/oauth/accesstokens/%s", oauthTokenName)
	resp, err := clientSet.Etcd.Get(timeout, prefix, clientv3.WithPrefix())
	require.NoError(t, err)
	require.Equalf(t, 1, len(resp.Kvs), "Expected to get a single key from etcd for OAuthAccessToken, got %d", len(resp.Kvs))
	return string(resp.Kvs[0].Value)
}

func CreateAndStoreOAuthTokenOfLife(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
	t.Helper()
	dynClient := getAuthDynamicClient(t)
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
			"apiVersion":               "oauth.openshift.io/v1",
			"kind":                     "OAuthAccessToken",
			"metadata":                 map[string]interface{}{"name": oauthTokenName},
			"clientName":               "console",
			"userName":                 "kube:admin",
			"userUID":                  "non-existing-user-id",
			"redirectURI":              "redirect.me.to.token.of.life",
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
	if strings.Contains(rawValue, "kube:admin") {
		t.Errorf("The OAuthAccessToken in etcd contains plain text 'kube:admin', content: %s", rawValue)
	}
}

func AssertOAuthTokenOfLifeNotEncrypted(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
	t.Helper()
	rawValue := GetRawOAuthTokenOfLife(t, clientSet, "")
	if !strings.Contains(rawValue, "kube:admin") {
		t.Errorf("The OAuthAccessToken in etcd doesn't have 'kube:admin', content: %s", rawValue)
	}
}

func AssertOAuthTokens(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertOAuthAccessTokens(t, clientSet.Etcd, string(expectedMode))
	assertOAuthAuthorizeTokens(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, AuthTargetGRs, namespace, labelSelector)
}

func assertOAuthAccessTokens(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all OAuthAccessTokens were encrypted/decrypted for %q mode", expectedMode)
	total, err := library.VerifyResources(t, etcdClient, "/openshift.io/oauth/accesstokens/", expectedMode, true)
	t.Logf("Verified %d OAuthAccessTokens", total)
	require.NoError(t, err)
}

func assertOAuthAuthorizeTokens(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all OAuthAuthorizeTokens were encrypted/decrypted for %q mode", expectedMode)
	total, err := library.VerifyResources(t, etcdClient, "/openshift.io/oauth/authorizetokens/", expectedMode, true)
	t.Logf("Verified %d OAuthAuthorizeTokens", total)
	require.NoError(t, err)
}
