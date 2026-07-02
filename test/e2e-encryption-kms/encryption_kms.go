package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionOnOff [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionOnOff(ctx, g.GinkgoTB())
	})

	g.It("TestKMSEncryptionProvidersMigration [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m]", func(ctx context.Context) {
		testKMSEncryptionProvidersMigration(ctx, g.GinkgoTB())
	})

})

// operatorConfig describes one encryption controller to verify.
type operatorConfig struct {
	basic              library.BasicScenario
	createResource     func(t testing.TB, cs library.ClientSet)
	assertEncrypted    func(t testing.TB, cs library.ClientSet)
	assertNotEncrypted func(t testing.TB, cs library.ClientSet)
}

// platformOperators returns the configs for every encryption controller
// tested by this suite:
//   - KAS-O  (cluster-kube-apiserver-operator):      secrets and configmaps
//   - Auth-O (cluster-authentication-operator):       oauth access/authorize tokens
//   - OAS-O  (cluster-openshift-apiserver-operator):  routes
//
// The first entry is the primary — its BasicScenario drives SetAndWaitForEncryptionType.
func platformOperators(ctx context.Context, routeNs string) []operatorConfig {
	kasNs := operatorclient.GlobalMachineSpecifiedConfigNamespace
	return []operatorConfig{
		{
			basic: library.BasicScenario{
				Namespace:                       kasNs,
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component=" + operatorclient.TargetNamespace,
				EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
				EncryptionConfigSecretNamespace: kasNs,
				OperatorNamespace:               operatorclient.OperatorNamespace,
				TargetGRs:                       operatorencryption.DefaultTargetGRs,
				AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
			},
			createResource: func(t testing.TB, cs library.ClientSet) {
				operatorencryption.CreateAndStoreSecretOfLife(t, cs, kasNs)
			},
			assertEncrypted: func(t testing.TB, cs library.ClientSet) {
				operatorencryption.AssertSecretOfLifeEncrypted(t, cs, operatorencryption.SecretOfLife(t, kasNs))
			},
			assertNotEncrypted: func(t testing.TB, cs library.ClientSet) {
				operatorencryption.AssertSecretOfLifeNotEncrypted(t, cs, operatorencryption.SecretOfLife(t, kasNs))
			},
		},
		{
			basic: library.BasicScenario{
				Namespace:                       "openshift-config-managed",
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component=openshift-oauth-apiserver",
				EncryptionConfigSecretName:      "encryption-config-openshift-oauth-apiserver",
				EncryptionConfigSecretNamespace: "openshift-config-managed",
				OperatorNamespace:               "openshift-authentication-operator",
				TargetGRs:                       library.AuthTargetGRs,
				AssertFunc:                      library.AssertTokens,
			},
			createResource: func(t testing.TB, cs library.ClientSet) {
				library.CreateAndStoreTokenOfLife(ctx, t, cs)
			},
			assertEncrypted: func(t testing.TB, cs library.ClientSet) {
				library.AssertTokenOfLifeEncrypted(t, cs, library.TokenOfLife(t, ""))
			},
			assertNotEncrypted: func(t testing.TB, cs library.ClientSet) {
				library.AssertTokenOfLifeNotEncrypted(t, cs, library.TokenOfLife(t, ""))
			},
		},
		{
			basic: library.BasicScenario{
				Namespace:                       kasNs,
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component=openshift-apiserver",
				EncryptionConfigSecretName:      "encryption-config-openshift-apiserver",
				EncryptionConfigSecretNamespace: kasNs,
				OperatorNamespace:               "openshift-apiserver-operator",
				TargetGRs:                       library.OASTargetGRs,
				AssertFunc:                      library.AssertRoutes,
			},
			createResource: func(t testing.TB, cs library.ClientSet) {
				library.CreateAndStoreRouteOfLife(ctx, t, cs, routeNs)
			},
			assertEncrypted: func(t testing.TB, cs library.ClientSet) {
				library.AssertRouteOfLifeEncrypted(t, cs, library.RouteOfLife(t, routeNs))
			},
			assertNotEncrypted: func(t testing.TB, cs library.ClientSet) {
				library.AssertRouteOfLifeNotEncrypted(t, cs, library.RouteOfLife(t, routeNs))
			},
		},
	}
}

// platformScenario holds the shared scenario configuration built from
// operatorConfigs. Both OnOff and ProvidersMigration tests consume it.
type platformScenario struct {
	BasicScenario                  library.BasicScenario
	CreateResourceFunc             func(t testing.TB, clientSet library.ClientSet, namespace string) runtime.Object
	AssertResourceEncryptedFunc    func(t testing.TB, clientSet library.ClientSet, resource runtime.Object)
	AssertResourceNotEncryptedFunc func(t testing.TB, clientSet library.ClientSet, resource runtime.Object)
	ResourceFunc                   func(t testing.TB, namespace string) runtime.Object
	ResourceName                   string
	EncryptionProvider             library.EncryptionProvider
}

// newPlatformScenario builds a composite scenario from the given operator
// configs. ops[0] is the primary — its BasicScenario drives
// SetAndWaitForEncryptionType; the remaining operators are asserted directly
// (they complete migration during the primary's wait time).
func newPlatformScenario(provider library.EncryptionProvider, ops []operatorConfig) platformScenario {
	primary := ops[0]

	compositeBasic := primary.basic
	compositeBasic.AssertFunc = func(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
		primary.basic.AssertFunc(t, clientSet, expectedMode, namespace, labelSelector)
		for _, op := range ops[1:] {
			op.basic.AssertFunc(t, clientSet, expectedMode, op.basic.Namespace, op.basic.LabelSelector)
			if t.Failed() {
				return
			}
		}
	}

	return platformScenario{
		BasicScenario: compositeBasic,
		CreateResourceFunc: func(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
			for _, op := range ops {
				op.createResource(t, clientSet)
			}
			return nil
		},
		AssertResourceEncryptedFunc: func(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
			for _, op := range ops {
				op.assertEncrypted(t, clientSet)
			}
		},
		AssertResourceNotEncryptedFunc: func(t testing.TB, clientSet library.ClientSet, _ runtime.Object) {
			for _, op := range ops {
				op.assertNotEncrypted(t, clientSet)
			}
		},
		ResourceFunc:       operatorencryption.SecretOfLife,
		ResourceName:       "PlatformResources",
		EncryptionProvider: provider,
	}
}

// testKMSEncryptionOnOff tests KMS encryption on/off cycle as a single test
// case covering the entire platform (KAS-O, Auth-O, OAS-O).
// This test:
// 1. Deploys the real Vault KMS plugin
// 2. Creates test resources (SecretOfLife, TokenOfLife, RouteOfLife)
// 3. Enables KMS encryption
// 4. Verifies all resources are encrypted
// 5. Disables encryption (Identity)
// 6. Verifies all resources are NOT encrypted
// 7. Re-enables KMS encryption
// 8. Verifies all resources are encrypted again
// 9. Disables encryption (Identity) again
// 10. Verifies all resources are NOT encrypted again
func testKMSEncryptionOnOff(ctx context.Context, t testing.TB) {
	provider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	cs := library.GetClients(t)
	routeNamespace, err := cs.Kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-routes-"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	routeNs := routeNamespace.Name
	t.Cleanup(func() {
		cs.Kube.CoreV1().Namespaces().Delete(context.Background(), routeNs, metav1.DeleteOptions{})
	})

	ps := newPlatformScenario(provider, platformOperators(ctx, routeNs))
	library.TestEncryptionTurnOnAndOff(ctx, t, library.OnOffScenario{
		BasicScenario:                  ps.BasicScenario,
		CreateResourceFunc:             ps.CreateResourceFunc,
		AssertResourceEncryptedFunc:    ps.AssertResourceEncryptedFunc,
		AssertResourceNotEncryptedFunc: ps.AssertResourceNotEncryptedFunc,
		ResourceFunc:                   ps.ResourceFunc,
		ResourceName:                   ps.ResourceName,
		EncryptionProvider:             ps.EncryptionProvider,
	})
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES
// encryption providers as a single test case covering the entire platform.
// This test:
// 1. Deploys the real Vault KMS plugin
// 2. Creates test resources (SecretOfLife, TokenOfLife, RouteOfLife)
// 3. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 4. Shuffles the selected AES provider with KMS to create a randomized migration order
// 5. Migrates between the providers in the shuffled order
// 6. Verifies all resources are correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	provider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	cs := library.GetClients(t)
	routeNamespace, err := cs.Kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-routes-"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	routeNs := routeNamespace.Name
	t.Cleanup(func() {
		cs.Kube.CoreV1().Namespaces().Delete(context.Background(), routeNs, metav1.DeleteOptions{})
	})

	ps := newPlatformScenario(provider, platformOperators(ctx, routeNs))
	library.TestEncryptionProvidersMigration(ctx, t, library.ProvidersMigrationScenario{
		BasicScenario:                  ps.BasicScenario,
		CreateResourceFunc:             ps.CreateResourceFunc,
		AssertResourceEncryptedFunc:    ps.AssertResourceEncryptedFunc,
		AssertResourceNotEncryptedFunc: ps.AssertResourceNotEncryptedFunc,
		ResourceFunc:                   ps.ResourceFunc,
		ResourceName:                   ps.ResourceName,
		EncryptionProviders: library.ShuffleEncryptionProviders([]library.EncryptionProvider{
			ps.EncryptionProvider,
			library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))],
		}),
	})
}
