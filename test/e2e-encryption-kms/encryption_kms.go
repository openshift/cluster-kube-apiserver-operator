package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
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

// testKMSEncryptionOnOff tests KMS encryption on/off cycle.
// This test:
// 1. Enables KMS encryption
// 2. Verifies secret is encrypted
// 3. Disables encryption (Identity)
// 4. Verifies secret is NOT encrypted
// 5. Re-enables KMS encryption
// 6. Verifies secret is encrypted again
// 7. Disables encryption (Identity) again
// 8. Verifies secret is NOT encrypted again
func testKMSEncryptionOnOff(ctx context.Context, t testing.TB) {
	library.TestEncryptionTurnOnAndOff(ctx, t, library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		CreateResourceFunc:             library.CreateAndStoreWellKnownSecretOfLife,
		AssertResourceEncryptedFunc:    library.AssertWellKnownSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: library.AssertWellKnownSecretOfLifeNotEncrypted,
		ResourceFunc:                   library.WellKnownSecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider:             librarykms.DefaultVaultEncryptionProvider(ctx, t),
	})
}

// testKMSEncryptionProvidersMigration tests migration between KMS and AES encryption providers
// across kube-apiserver, oauth-apiserver, and openshift-apiserver operators.
// This test:
// 1. Creates SecretOfLife, TokenOfLife, and RouteOfLife test resources
// 2. Randomly picks one AES encryption provider (AESGCM or AESCBC)
// 3. Shuffles the selected AES provider with KMS to create a randomized migration order
// 4. Applies one cluster-wide APIServer config update per step and waits per operator in parallel
// 5. Verifies each resource is correctly encrypted after each migration
func testKMSEncryptionProvidersMigration(ctx context.Context, t testing.TB) {
	providers := library.ShuffleEncryptionProviders([]library.EncryptionProvider{
		librarykms.DefaultVaultEncryptionProvider(ctx, t),
		library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))],
	})

	library.TestEncryptionProvidersMigration(ctx, t,
		library.ProvidersMigrationScenario{
			BasicScenario: library.BasicScenario{
				Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
				EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
				EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				OperatorNamespace:               operatorclient.OperatorNamespace,
				TargetGRs:                       library.WellKnownKASTargetGRs,
				AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
			},
			CreateResourceFunc:             library.CreateAndStoreWellKnownSecretOfLife,
			AssertResourceEncryptedFunc:    library.AssertWellKnownSecretOfLifeEncrypted,
			AssertResourceNotEncryptedFunc: library.AssertWellKnownSecretOfLifeNotEncrypted,
			ResourceFunc:                   library.WellKnownSecretOfLife,
			ResourceName:                   "SecretOfLife",
			EncryptionProviders:            providers,
		},
		library.ProvidersMigrationScenario{
			BasicScenario: library.BasicScenario{
				Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + "openshift-oauth-apiserver",
				EncryptionConfigSecretName:      "encryption-config-openshift-oauth-apiserver",
				EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				OperatorNamespace:               "openshift-authentication-operator",
				TargetGRs:                       library.WellKnownAuthTargetGRs,
				AssertFunc:                      library.AssertWellKnownTokens,
			},
			CreateResourceFunc: func(t testing.TB, clientSet library.ClientSet, _ string) runtime.Object {
				return library.CreateAndStoreWellKnownTokenOfLife(ctx, t, clientSet)
			},
			AssertResourceEncryptedFunc:    library.AssertWellKnownTokenOfLifeEncrypted,
			AssertResourceNotEncryptedFunc: library.AssertWellKnownTokenOfLifeNotEncrypted,
			ResourceFunc:                   library.WellKnownTokenOfLife,
			ResourceName:                   "TokenOfLife",
		},
		library.ProvidersMigrationScenario{
			BasicScenario: library.BasicScenario{
				Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
				LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + "openshift-apiserver",
				EncryptionConfigSecretName:      "encryption-config-openshift-apiserver",
				EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				OperatorNamespace:               "openshift-apiserver-operator",
				TargetGRs:                       library.WellKnownOASTargetGRs,
				AssertFunc:                      library.AssertWellKnownRoutes,
			},
			CreateResourceFunc: func(t testing.TB, clientSet library.ClientSet, ns string) runtime.Object {
				return library.CreateAndStoreWellKnownRouteOfLife(ctx, t, clientSet, ns)
			},
			AssertResourceEncryptedFunc:    library.AssertWellKnownRouteOfLifeEncrypted,
			AssertResourceNotEncryptedFunc: library.AssertWellKnownRouteOfLifeNotEncrypted,
			ResourceFunc:                   library.WellKnownRouteOfLife,
			ResourceName:                   "RouteOfLife",
		},
	)
}
