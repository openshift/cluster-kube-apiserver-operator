package e2e_encryption_kms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
	"k8s.io/apimachinery/pkg/runtime"
)

// assertAllOperatorsEncryptionState checks that all operators (KAS-O, OAS-O, Auth-O)
// have the expected encryption mode applied.
func assertAllOperatorsEncryptionState(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
	t.Helper()

	// KAS-O
	operatorencryption.AssertSecretsAndConfigMaps(t, clientSet, expectedMode, namespace, labelSelector)

	// OAS-O
	operatorencryption.AssertOASRoutes(t, clientSet, expectedMode,
		operatorclient.GlobalMachineSpecifiedConfigNamespace, operatorencryption.OASLabelSelector)

	// Auth-O
	operatorencryption.AssertOAuthTokens(t, clientSet, expectedMode,
		operatorclient.GlobalMachineSpecifiedConfigNamespace, operatorencryption.AuthLabelSelector)
}

// createAllResources creates test resources for all operators and returns the KAS-O secret.
func createAllResources(t testing.TB, clientSet library.ClientSet, namespace string) runtime.Object {
	t.Helper()

	// OAS-O
	operatorencryption.CreateAndStoreOASRouteOfLife(t, clientSet, operatorclient.GlobalMachineSpecifiedConfigNamespace)

	// Auth-O
	operatorencryption.CreateAndStoreOAuthTokenOfLife(t, clientSet, operatorclient.GlobalMachineSpecifiedConfigNamespace)

	// KAS-O (returned as the primary resource)
	return operatorencryption.CreateAndStoreSecretOfLife(t, clientSet, namespace)
}

// assertAllResourcesEncrypted checks that test resources from all operators are encrypted.
func assertAllResourcesEncrypted(t testing.TB, clientSet library.ClientSet, resource runtime.Object) {
	t.Helper()
	operatorencryption.AssertSecretOfLifeEncrypted(t, clientSet, resource)
	operatorencryption.AssertOASRouteOfLifeEncrypted(t, clientSet, nil)
	operatorencryption.AssertOAuthTokenOfLifeEncrypted(t, clientSet, nil)
}

// assertAllResourcesNotEncrypted checks that test resources from all operators are NOT encrypted.
func assertAllResourcesNotEncrypted(t testing.TB, clientSet library.ClientSet, resource runtime.Object) {
	t.Helper()
	operatorencryption.AssertSecretOfLifeNotEncrypted(t, clientSet, resource)
	operatorencryption.AssertOASRouteOfLifeNotEncrypted(t, clientSet, nil)
	operatorencryption.AssertOAuthTokenOfLifeNotEncrypted(t, clientSet, nil)
}

func TestKMSEncryptionOnOff(t *testing.T) {
	librarykms.DeployUpstreamMockKMSPlugin(context.Background(), t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)
	library.TestEncryptionTurnOnAndOff(t, library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      assertAllOperatorsEncryptionState,
		},
		CreateResourceFunc:             createAllResources,
		AssertResourceEncryptedFunc:    assertAllResourcesEncrypted,
		AssertResourceNotEncryptedFunc: assertAllResourcesNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider:             configv1.EncryptionTypeKMS,
	})
}

func TestKMSEncryptionProvidersMigration(t *testing.T) {
	librarykms.DeployUpstreamMockKMSPlugin(context.Background(), t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)
	library.TestEncryptionProvidersMigration(t, library.ProvidersMigrationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      assertAllOperatorsEncryptionState,
		},
		CreateResourceFunc:             createAllResources,
		AssertResourceEncryptedFunc:    assertAllResourcesEncrypted,
		AssertResourceNotEncryptedFunc: assertAllResourcesNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProviders:            library.ShuffleEncryptionProviders([]configv1.EncryptionType{configv1.EncryptionTypeKMS, library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))]}),
	})
}
