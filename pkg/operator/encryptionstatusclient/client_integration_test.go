//go:build integration

package encryptionstatusclient_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/encryptionstatusclient"
)

const kubeAPIServerCRDName = "kubeapiservers.operator.openshift.io"

// Package-level clients initialised once in TestMain and reused across tests.
// nil when KUBECONFIG is unset; individual tests skip in that case.
var (
	testAPIExtClient apiextensionsclient.Interface
	testOpClient     *operatorclient.Clientset
	// crdOwnedByTests is true when TestMain created the CRD and should delete it.
	crdOwnedByTests bool
)

// TestMain creates the KubeAPIServer CRD once before all tests and tears it
// down once after. Per-test setup only manages the KubeAPIServer/cluster
// singleton, avoiding the race where one test's CRD cleanup races with the
// next test's object creation.
func TestMain(m *testing.M) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		// No cluster — individual tests will skip themselves.
		os.Exit(m.Run())
	}

	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build rest config: %v\n", err)
		os.Exit(1)
	}

	testAPIExtClient, err = apiextensionsclient.NewForConfig(restCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build apiextensions client: %v\n", err)
		os.Exit(1)
	}

	testOpClient, err = operatorclient.NewForConfig(restCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build operator client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	crdOwnedByTests, err = ensureKubeAPIServerCRD(ctx, testAPIExtClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ensure KubeAPIServer CRD: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if crdOwnedByTests {
		_ = testAPIExtClient.ApiextensionsV1().CustomResourceDefinitions().Delete(
			ctx, kubeAPIServerCRDName, metav1.DeleteOptions{})
	}

	os.Exit(code)
}

// TestKMSEncryptionStatusClientMultipleHealthReporters verifies that multiple
// health reporters — one per node — each own their own entry in the HealthReports
// map list and do not clobber each other's entries on re-sync.
//
// HealthReports uses a +listType=map with listMapKeys [nodeName, keyId], so SSA
// tracks ownership per entry. Each reporter's field manager owns only the entries
// it submitted; updating one reporter's entry leaves the other reporters' entries
// untouched.
func TestKMSEncryptionStatusClientMultipleHealthReporters(t *testing.T) {
	if testOpClient == nil {
		t.Skip("KUBECONFIG not set")
	}

	ctx := context.Background()
	client := encryptionstatusclient.NewKubeAPIServerClient(testOpClient.OperatorV1())

	createClusterSingleton(t, ctx)

	const (
		fmNode1 = "health-reporter-node-1"
		fmNode2 = "health-reporter-node-2"
		fmNode3 = "health-reporter-node-3"
	)

	t.Run("each reporter applies its own entry independently", func(t *testing.T) {
		for _, tc := range []struct {
			fm       string
			nodeName string
		}{
			{fmNode1, "node-1"},
			{fmNode2, "node-2"},
			{fmNode3, "node-3"},
		} {
			err := client.ApplyKMSEncryptionStatus(ctx, tc.fm,
				applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
					applyoperatorv1.KMSPluginHealthReport().
						WithNodeName(tc.nodeName).
						WithKeyId("1").
						WithStatus(operatorv1.KMSPluginHealthStatusHealthy).
						WithLastCheckedTime(metav1.Now()).
						WithKEKId("kek-abc"),
				),
			)
			require.NoError(t, err, "reporter %s", tc.fm)
		}

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		require.Len(t, status.HealthReports, 3, "expected one entry per reporter")
	})

	t.Run("node-1 re-sync does not clobber node-2 or node-3 entries", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, fmNode1,
			applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
				applyoperatorv1.KMSPluginHealthReport().
					WithNodeName("node-1").
					WithKeyId("1").
					WithStatus(operatorv1.KMSPluginHealthStatusUnhealthy).
					WithLastCheckedTime(metav1.Now()).
					WithKEKId("kek-abc").
					WithDetail("timeout dialing plugin"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		require.Len(t, status.HealthReports, 3, "re-sync removed other reporters' entries")

		byNode := indexByNodeName(status.HealthReports)
		assert.Equal(t, operatorv1.KMSPluginHealthStatusUnhealthy, byNode["node-1"].Status, "node-1 status not updated")
		assert.Equal(t, operatorv1.KMSPluginHealthStatusHealthy, byNode["node-2"].Status, "node-2 entry clobbered")
		assert.Equal(t, operatorv1.KMSPluginHealthStatusHealthy, byNode["node-3"].Status, "node-3 entry clobbered")
	})

	t.Run("node-2 re-sync does not clobber node-1 or node-3 entries", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, fmNode2,
			applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
				applyoperatorv1.KMSPluginHealthReport().
					WithNodeName("node-2").
					WithKeyId("1").
					WithStatus(operatorv1.KMSPluginHealthStatusError).
					WithLastCheckedTime(metav1.Now()).
					WithKEKId("kek-abc").
					WithDetail("gRPC connection refused"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		require.Len(t, status.HealthReports, 3, "re-sync removed other reporters' entries")

		byNode := indexByNodeName(status.HealthReports)
		assert.Equal(t, operatorv1.KMSPluginHealthStatusUnhealthy, byNode["node-1"].Status, "node-1 entry clobbered")
		assert.Equal(t, operatorv1.KMSPluginHealthStatusError, byNode["node-2"].Status, "node-2 status not updated")
		assert.Equal(t, operatorv1.KMSPluginHealthStatusHealthy, byNode["node-3"].Status, "node-3 entry clobbered")
	})
}

func indexByNodeName(reports []operatorv1.KMSPluginHealthReport) map[string]operatorv1.KMSPluginHealthReport {
	m := make(map[string]operatorv1.KMSPluginHealthReport, len(reports))
	for _, r := range reports {
		m[r.NodeName] = r
	}
	return m
}

// TestKMSEncryptionStatusClientSSAOwnership verifies that three controllers
// using distinct SSA field managers can independently write to their respective
// sub-fields of KMSEncryptionStatus — HealthReports (health reporter),
// Preflight.ObservedConfigHash (key controller), and Preflight.Result
// (preflight controller) — without clobbering each other on every sync cycle.
//
// Run against a kind cluster:
//
//	go test -tags integration -v -count=1 ./pkg/operator/encryptionstatusclient/
func TestKMSEncryptionStatusClientSSAOwnership(t *testing.T) {
	if testOpClient == nil {
		t.Skip("KUBECONFIG not set")
	}

	ctx := context.Background()
	client := encryptionstatusclient.NewKubeAPIServerClient(testOpClient.OperatorV1())

	createClusterSingleton(t, ctx)

	const (
		healthReporterFM = "health-reporter-node-1"
		keyControllerFM  = "key-controller"
		preflightFM      = "preflight-controller"
	)

	t.Run("health reporter applies HealthReports", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, healthReporterFM,
			applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
				applyoperatorv1.KMSPluginHealthReport().
					WithNodeName("node-1").
					WithKeyId("1").
					WithStatus(operatorv1.KMSPluginHealthStatusHealthy).
					WithLastCheckedTime(metav1.Now()).
					WithKEKId("kek-abc"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		require.Len(t, status.HealthReports, 1)
		assert.Equal(t, "node-1", status.HealthReports[0].NodeName)
		assert.Equal(t, operatorv1.KMSPluginHealthStatusHealthy, status.HealthReports[0].Status)
		assert.Equal(t, "kek-abc", status.HealthReports[0].KEKId)
	})

	t.Run("key controller applies ObservedConfigHash without clobbering HealthReports", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, keyControllerFM,
			applyoperatorv1.KMSEncryptionStatus().WithPreflight(
				applyoperatorv1.KMSPreflightCheck().WithObservedConfigHash("k6dSVA=="),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash)
		require.Len(t, status.HealthReports, 1, "key controller clobbered HealthReports")
		assert.Equal(t, "node-1", status.HealthReports[0].NodeName)
	})

	t.Run("preflight controller applies Result without clobbering other fields", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, preflightFM,
			applyoperatorv1.KMSEncryptionStatus().WithPreflight(
				applyoperatorv1.KMSPreflightCheck().WithResult(
					applyoperatorv1.KMSPreflightResult().
						WithStatus(operatorv1.KMSPreflightResultSucceeded).
						WithConfigHash("k6dSVA==").
						WithRemoteKeyID("kek-abc"),
				),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, operatorv1.KMSPreflightResultSucceeded, status.Preflight.Result.Status)
		assert.Equal(t, "k6dSVA==", status.Preflight.Result.ConfigHash)
		assert.Equal(t, "kek-abc", status.Preflight.Result.RemoteKeyID)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash, "preflight controller clobbered ObservedConfigHash")
		require.Len(t, status.HealthReports, 1, "preflight controller clobbered HealthReports")
	})

	t.Run("health reporter re-sync does not clobber preflight fields", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, healthReporterFM,
			applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
				applyoperatorv1.KMSPluginHealthReport().
					WithNodeName("node-1").
					WithKeyId("1").
					WithStatus(operatorv1.KMSPluginHealthStatusUnhealthy).
					WithLastCheckedTime(metav1.Now()).
					WithKEKId("kek-abc").
					WithDetail("timeout dialing plugin"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		require.Len(t, status.HealthReports, 1)
		assert.Equal(t, operatorv1.KMSPluginHealthStatusUnhealthy, status.HealthReports[0].Status)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash, "health reporter clobbered ObservedConfigHash")
		assert.Equal(t, operatorv1.KMSPreflightResultSucceeded, status.Preflight.Result.Status, "health reporter clobbered Result")
	})

	t.Run("key controller hash update does not clobber health reports or result", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, keyControllerFM,
			applyoperatorv1.KMSEncryptionStatus().WithPreflight(
				applyoperatorv1.KMSPreflightCheck().WithObservedConfigHash("new-has"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, "new-has", status.Preflight.ObservedConfigHash)
		require.Len(t, status.HealthReports, 1, "key controller clobbered HealthReports")
		assert.Equal(t, operatorv1.KMSPreflightResultSucceeded, status.Preflight.Result.Status, "key controller clobbered Result")
	})
}

// TestKMSEncryptionStatusClientUpdate verifies that UpdateKMSEncryptionStatus
// uses plain read-modify-write semantics: the mutate function is applied to the
// live status and written back, leaving all other fields intact.
func TestKMSEncryptionStatusClientUpdate(t *testing.T) {
	if testOpClient == nil {
		t.Skip("KUBECONFIG not set")
	}

	ctx := context.Background()
	client := encryptionstatusclient.NewKubeAPIServerClient(testOpClient.OperatorV1())

	createClusterSingleton(t, ctx)

	// Seed HealthReports via SSA so we can verify Update doesn't clobber them.
	err := client.ApplyKMSEncryptionStatus(ctx, "health-reporter-node-1",
		applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
			applyoperatorv1.KMSPluginHealthReport().
				WithNodeName("node-1").
				WithKeyId("1").
				WithStatus(operatorv1.KMSPluginHealthStatusHealthy).
				WithLastCheckedTime(metav1.Now()).
				WithKEKId("kek-abc"),
		),
	)
	require.NoError(t, err)

	t.Run("Update sets Preflight.ObservedConfigHash", func(t *testing.T) {
		err := client.UpdateKMSEncryptionStatus(ctx, func(s *operatorv1.KMSEncryptionStatus) {
			s.Preflight.ObservedConfigHash = "k6dSVA=="
		})
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash)
		require.Len(t, status.HealthReports, 1, "Update clobbered SSA-written HealthReports")
	})

	t.Run("Update sets Preflight.Result", func(t *testing.T) {
		err := client.UpdateKMSEncryptionStatus(ctx, func(s *operatorv1.KMSEncryptionStatus) {
			s.Preflight.Result = operatorv1.KMSPreflightResult{
				Status:      operatorv1.KMSPreflightResultSucceeded,
				ConfigHash:  "k6dSVA==",
				RemoteKeyID: "kek-abc",
			}
		})
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, operatorv1.KMSPreflightResultSucceeded, status.Preflight.Result.Status)
		assert.Equal(t, "k6dSVA==", status.Preflight.Result.ConfigHash)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash, "second Update clobbered ObservedConfigHash")
		require.Len(t, status.HealthReports, 1, "second Update clobbered SSA-written HealthReports")
	})

	t.Run("SSA re-sync after Update does not clobber Update-written fields", func(t *testing.T) {
		err := client.ApplyKMSEncryptionStatus(ctx, "health-reporter-node-1",
			applyoperatorv1.KMSEncryptionStatus().WithHealthReports(
				applyoperatorv1.KMSPluginHealthReport().
					WithNodeName("node-1").
					WithKeyId("1").
					WithStatus(operatorv1.KMSPluginHealthStatusUnhealthy).
					WithLastCheckedTime(metav1.Now()).
					WithKEKId("kek-abc").
					WithDetail("timeout dialing plugin"),
			),
		)
		require.NoError(t, err)

		status, err := client.GetKMSEncryptionStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, operatorv1.KMSPluginHealthStatusUnhealthy, status.HealthReports[0].Status)
		assert.Equal(t, "k6dSVA==", status.Preflight.ObservedConfigHash, "SSA clobbered Update-written ObservedConfigHash")
		assert.Equal(t, operatorv1.KMSPreflightResultSucceeded, status.Preflight.Result.Status, "SSA clobbered Update-written Result")
	})
}

// createClusterSingleton creates KubeAPIServer/cluster and registers its
// deletion as a test cleanup, so each test starts with a clean object.
func createClusterSingleton(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := testOpClient.OperatorV1().KubeAPIServers().Create(ctx,
		&operatorv1.KubeAPIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
		metav1.CreateOptions{},
	)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create KubeAPIServer/cluster: %v", err)
	}
	t.Cleanup(func() {
		_ = testOpClient.OperatorV1().KubeAPIServers().Delete(ctx, "cluster", metav1.DeleteOptions{})
	})
}

// ensureKubeAPIServerCRD deletes any pre-existing KubeAPIServer CRD (e.g. a
// stale one left by a previous test run), then creates a fresh one with the
// schema needed by these tests — specifically x-kubernetes-list-type: map on
// healthReports so that SSA per-node list-entry ownership works correctly.
// It always returns owned=true because it always creates the CRD.
func ensureKubeAPIServerCRD(ctx context.Context, client apiextensionsclient.Interface) (owned bool, err error) {
	// Remove any stale CRD so we always start with the correct schema.
	if err = client.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, kubeAPIServerCRDName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("delete pre-existing KubeAPIServer CRD: %w", err)
	}
	if err = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			_, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, kubeAPIServerCRDName, metav1.GetOptions{})
			return apierrors.IsNotFound(err), nil
		},
	); err != nil {
		return false, fmt.Errorf("wait for CRD termination: %w", err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: kubeAPIServerCRDName},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "operator.openshift.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "KubeAPIServer",
				ListKind: "KubeAPIServerList",
				Plural:   "kubeapiservers",
				Singular: "kubeapiserver",
			},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: ptr.To(true),
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"status": {
								Type:                   "object",
								XPreserveUnknownFields: ptr.To(true),
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"encryptionStatus": {
										Type:                   "object",
										XPreserveUnknownFields: ptr.To(true),
										Properties: map[string]apiextensionsv1.JSONSchemaProps{
											// x-kubernetes-list-type: map is required for SSA to
											// merge list entries by key rather than replacing the
											// whole list atomically. Without it, each Apply would
											// replace all entries, leaving only the last writer's.
											"healthReports": {
												Type:         "array",
												XListType:    ptr.To("map"),
												XListMapKeys: []string{"nodeName", "keyId"},
												Items: &apiextensionsv1.JSONSchemaPropsOrArray{
													Schema: &apiextensionsv1.JSONSchemaProps{
														Type:                   "object",
														XPreserveUnknownFields: ptr.To(true),
														Required:               []string{"nodeName", "keyId"},
														Properties: map[string]apiextensionsv1.JSONSchemaProps{
															"nodeName": {Type: "string"},
															"keyId":    {Type: "string"},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				Subresources: &apiextensionsv1.CustomResourceSubresources{
					Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
				},
			}},
		},
	}

	if _, err = client.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{}); err != nil {
		return false, fmt.Errorf("create KubeAPIServer CRD: %w", err)
	}

	return true, wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			got, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, kubeAPIServerCRDName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for _, c := range got.Status.Conditions {
				if c.Type == apiextensionsv1.Established && c.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		},
	)
}
