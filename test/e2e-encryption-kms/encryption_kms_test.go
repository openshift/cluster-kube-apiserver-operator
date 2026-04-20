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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type operatorTestConfig struct {
	name                       string
	namespace                  string
	labelSelector              string
	encryptionConfigSecretName string
	operatorNamespace          string
	targetGRs                  []schema.GroupResource
	assertFunc                 func(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string)
	createResourceFunc         func(t testing.TB, clientSet library.ClientSet, namespace string) runtime.Object
	assertEncryptedFunc        func(t testing.TB, clientSet library.ClientSet, resource runtime.Object)
	assertNotEncryptedFunc     func(t testing.TB, clientSet library.ClientSet, resource runtime.Object)
	resourceFunc               func(t testing.TB, namespace string) runtime.Object
	resourceName               string
}

var allOperators = []operatorTestConfig{
	{
		name:                       "KASO",
		namespace:                  operatorclient.GlobalMachineSpecifiedConfigNamespace,
		labelSelector:              "encryption.apiserver.operator.openshift.io/component=" + operatorclient.TargetNamespace,
		encryptionConfigSecretName: fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		operatorNamespace:          operatorclient.OperatorNamespace,
		targetGRs:                  operatorencryption.DefaultTargetGRs,
		assertFunc:                 operatorencryption.AssertSecretsAndConfigMaps,
		createResourceFunc:         operatorencryption.CreateAndStoreSecretOfLife,
		assertEncryptedFunc:        operatorencryption.AssertSecretOfLifeEncrypted,
		assertNotEncryptedFunc:     operatorencryption.AssertSecretOfLifeNotEncrypted,
		resourceFunc:               operatorencryption.SecretOfLife,
		resourceName:               "SecretOfLife",
	},
	{
		name:                       "OASO",
		namespace:                  operatorclient.GlobalMachineSpecifiedConfigNamespace,
		labelSelector:              operatorencryption.OASLabelSelector,
		encryptionConfigSecretName: operatorencryption.OASEncryptionConfigSecretName,
		operatorNamespace:          operatorencryption.OASOperatorNamespace,
		targetGRs:                  operatorencryption.OASTargetGRs,
		assertFunc:                 operatorencryption.AssertOASSecretsAndConfigMaps,
		createResourceFunc:         operatorencryption.CreateAndStoreOASSecretOfLife,
		assertEncryptedFunc:        operatorencryption.AssertOASSecretOfLifeEncrypted,
		assertNotEncryptedFunc:     operatorencryption.AssertOASSecretOfLifeNotEncrypted,
		resourceFunc:               operatorencryption.OASSecretOfLife,
		resourceName:               "OASSecretOfLife",
	},
	{
		name:                       "AuthO",
		namespace:                  operatorclient.GlobalMachineSpecifiedConfigNamespace,
		labelSelector:              operatorencryption.AuthLabelSelector,
		encryptionConfigSecretName: operatorencryption.AuthEncryptionConfigSecretName,
		operatorNamespace:          operatorencryption.AuthOperatorNamespace,
		targetGRs:                  operatorencryption.AuthTargetGRs,
		assertFunc:                 operatorencryption.AssertOAuthTokens,
		createResourceFunc:         operatorencryption.CreateAndStoreOAuthTokenOfLife,
		assertEncryptedFunc:        operatorencryption.AssertOAuthTokenOfLifeEncrypted,
		assertNotEncryptedFunc:     operatorencryption.AssertOAuthTokenOfLifeNotEncrypted,
		resourceFunc:               operatorencryption.OAuthTokenOfLife,
		resourceName:               "OAuthTokenOfLife",
	},
}

// setEncryptionAndWaitForAllOperators sets the encryption type once (cluster-wide)
// and waits for all operators to complete key migration.
func setEncryptionAndWaitForAllOperators(t *testing.T, encryptionType configv1.EncryptionType) {
	t.Helper()
	for _, op := range allOperators {
		t.Run("WaitFor_"+op.name, func(t *testing.T) {
			library.SetAndWaitForEncryptionType(t, encryptionType, op.targetGRs, op.namespace, op.labelSelector)
			op.assertFunc(t, library.GetClients(t), encryptionType, op.namespace, op.labelSelector)
		})
	}
}

// TestKMSEncryptionOnOff tests KMS encryption on/off for the entire platform.
// It sets encryption once and verifies all operators (KAS-O, OAS-O, Auth-O)
// encrypted/decrypted their resources correctly, avoiding redundant on/off cycles.
func TestKMSEncryptionOnOff(t *testing.T) {
	librarykms.DeployUpstreamMockKMSPlugin(context.Background(), t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)

	e := library.NewE(t)
	clientSet := library.GetClients(e)

	// Step 1: Create test resources for all operators
	t.Run("CreateResources", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.createResourceFunc(t, clientSet, op.namespace)
			})
		}
	})
	if t.Failed() {
		return
	}

	// Step 2: Enable KMS encryption (one toggle for the whole platform)
	t.Run("EnableKMS", func(t *testing.T) {
		setEncryptionAndWaitForAllOperators(t, configv1.EncryptionTypeKMS)
	})
	if t.Failed() {
		return
	}

	// Step 3: Verify all resources are encrypted
	t.Run("AssertEncrypted", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.assertEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
			})
		}
	})
	if t.Failed() {
		return
	}

	// Step 4: Disable encryption (one toggle for the whole platform)
	t.Run("DisableEncryption", func(t *testing.T) {
		setEncryptionAndWaitForAllOperators(t, configv1.EncryptionTypeIdentity)
	})
	if t.Failed() {
		return
	}

	// Step 5: Verify all resources are NOT encrypted
	t.Run("AssertNotEncrypted", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.assertNotEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
			})
		}
	})
	if t.Failed() {
		return
	}

	// Step 6: Re-enable KMS encryption
	t.Run("ReEnableKMS", func(t *testing.T) {
		setEncryptionAndWaitForAllOperators(t, configv1.EncryptionTypeKMS)
	})
	if t.Failed() {
		return
	}

	// Step 7: Verify all resources are encrypted again
	t.Run("AssertEncryptedAgain", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.assertEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
			})
		}
	})
	if t.Failed() {
		return
	}

	// Step 8: Disable encryption again
	t.Run("DisableEncryptionAgain", func(t *testing.T) {
		setEncryptionAndWaitForAllOperators(t, configv1.EncryptionTypeIdentity)
	})
	if t.Failed() {
		return
	}

	// Step 9: Verify all resources are NOT encrypted again
	t.Run("AssertNotEncryptedAgain", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.assertNotEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
			})
		}
	})
}

// TestKMSEncryptionProvidersMigration tests migration between KMS and a randomly
// selected AES encryption provider for the entire platform. It sets encryption once
// per provider and verifies all operators migrated correctly.
func TestKMSEncryptionProvidersMigration(t *testing.T) {
	librarykms.DeployUpstreamMockKMSPlugin(context.Background(), t, library.GetClients(t).Kube, librarykms.WellKnownUpstreamMockKMSPluginNamespace, librarykms.WellKnownUpstreamMockKMSPluginImage, librarykms.DefaultKMSPluginCount)

	e := library.NewE(t)
	clientSet := library.GetClients(e)

	providers := library.ShuffleEncryptionProviders([]configv1.EncryptionType{
		configv1.EncryptionTypeKMS,
		library.SupportedStaticEncryptionProviders[rand.IntN(len(library.SupportedStaticEncryptionProviders))],
	})

	// Step 1: Create test resources for all operators
	t.Run("CreateResources", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.createResourceFunc(t, clientSet, op.namespace)
			})
		}
	})
	if t.Failed() {
		return
	}

	// Step 2: Migrate through each provider in sequence
	for i, provider := range providers {
		prefix := "EncryptWith"
		if i > 0 {
			prefix = "MigrateTo"
		}
		stepName := fmt.Sprintf("%s_%s", prefix, string(provider))

		t.Run(stepName, func(t *testing.T) {
			setEncryptionAndWaitForAllOperators(t, provider)
		})
		if t.Failed() {
			return
		}

		t.Run(fmt.Sprintf("AssertEncrypted_%s", string(provider)), func(t *testing.T) {
			for _, op := range allOperators {
				t.Run(op.name, func(t *testing.T) {
					op.assertEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
				})
			}
		})
		if t.Failed() {
			return
		}
	}

	// Step 3: Disable encryption and verify all resources are not encrypted
	t.Run("DisableEncryption", func(t *testing.T) {
		setEncryptionAndWaitForAllOperators(t, configv1.EncryptionTypeIdentity)
	})
	if t.Failed() {
		return
	}

	t.Run("AssertNotEncrypted", func(t *testing.T) {
		for _, op := range allOperators {
			t.Run(op.name, func(t *testing.T) {
				op.assertNotEncryptedFunc(t, clientSet, op.resourceFunc(t, op.namespace))
			})
		}
	})
}
