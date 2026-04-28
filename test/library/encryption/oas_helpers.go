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
	OASTargetNamespace   = "openshift-apiserver"
	OASOperatorNamespace = "openshift-apiserver-operator"
	oasRouteName         = "route-of-life"
	oasRouteNamespace    = "openshift-apiserver"
)

var OASTargetGRs = []schema.GroupResource{
	{Group: "route.openshift.io", Resource: "routes"},
}

var OASLabelSelector = "encryption.apiserver.operator.openshift.io/component=" + OASTargetNamespace
var OASEncryptionConfigSecretName = fmt.Sprintf("encryption-config-%s", OASTargetNamespace)

var routeGVR = schema.GroupVersionResource{
	Group:    "route.openshift.io",
	Version:  "v1",
	Resource: "routes",
}

func getOASDynamicClient(t testing.TB) dynamic.Interface {
	t.Helper()
	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	dynClient, err := dynamic.NewForConfig(kubeConfig)
	require.NoError(t, err)
	return dynClient
}

func GetRawOASRouteOfLife(t testing.TB, clientSet library.ClientSet, namespace string) string {
	t.Helper()
	timeout, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	prefix := fmt.Sprintf("/openshift.io/routes/%s/%s", namespace, oasRouteName)
	resp, err := clientSet.Etcd.Get(timeout, prefix, clientv3.WithPrefix())
	require.NoError(t, err)
	require.Equalf(t, 1, len(resp.Kvs), "Expected to get a single key from etcd for Route, got %d", len(resp.Kvs))
	return string(resp.Kvs[0].Value)
}

func CreateAndStoreOASRouteOfLife(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
	t.Helper()
	dynClient := getOASDynamicClient(t)
	routeClient := dynClient.Resource(routeGVR).Namespace(oasRouteNamespace)

	_, err := routeClient.Get(context.TODO(), oasRouteName, metav1.GetOptions{})
	if err == nil {
		t.Log("The OAS Route already exists, removing it first")
		err := routeClient.Delete(context.TODO(), oasRouteName, metav1.DeleteOptions{})
		if err != nil {
			t.Errorf("Failed to delete Route %s: %v", oasRouteName, err)
		}
	} else if !errors.IsNotFound(err) {
		t.Errorf("Failed to check if Route exists: %v", err)
	}

	t.Logf("Creating Route %q in %s namespace", oasRouteName, oasRouteNamespace)
	route := OASRouteOfLife(t, oasRouteNamespace)
	created, err := routeClient.Create(context.TODO(), route.(*unstructured.Unstructured), metav1.CreateOptions{})
	require.NoError(t, err)
	return created
}

func OASRouteOfLife(t testing.TB, namespace string) runtime.Object {
	t.Helper()
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name":      oasRouteName,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"host": "devcluster.openshift.io",
				"port": map[string]interface{}{
					"targetPort": float64(2014),
				},
				"to": map[string]interface{}{
					"name": "dummyroute",
				},
			},
		},
	}
}

func AssertOASRouteOfLifeEncrypted(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
	t.Helper()
	rawValue := GetRawOASRouteOfLife(t, clientSet, oasRouteNamespace)
	if strings.Contains(rawValue, "dummyroute") {
		t.Errorf("Route not encrypted, route received from etcd has %q (plain text), raw content in etcd is %s", "dummyroute", rawValue)
	}
}

func AssertOASRouteOfLifeNotEncrypted(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
	t.Helper()
	rawValue := GetRawOASRouteOfLife(t, clientSet, oasRouteNamespace)
	if !strings.Contains(rawValue, "dummyroute") {
		t.Errorf("Route received from etcd doesn't have %q (plain text), raw content in etcd is %s", "dummyroute", rawValue)
	}
}

func AssertOASRoutes(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()
	assertRoutes(t, clientSet.Etcd, string(expectedMode))
	library.AssertLastMigratedKey(t, clientSet.Kube, OASTargetGRs, namespace, labelSelector)
}

func assertRoutes(t testing.TB, etcdClient library.EtcdClient, expectedMode string) {
	t.Logf("Checking if all Routes were encrypted/decrypted for %q mode", expectedMode)
	totalRoutes, err := library.VerifyResources(t, etcdClient, "/openshift.io/routes/", expectedMode, false)
	t.Logf("Verified %d Routes", totalRoutes)
	require.NoError(t, err)
}
