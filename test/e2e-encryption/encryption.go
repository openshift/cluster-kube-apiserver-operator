package e2e_encryption

import (
	"flag"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

var provider = flag.String("provider", "aescbc", "encryption provider used by the tests")

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator encryption", func() {
	g.It("TestEncryptionTypeIdentity [Serial]", func() {
		TestEncryptionTypeIdentity(g.GinkgoTB())
	})

	g.It("TestEncryptionTypeUnset [Serial]", func() {
		TestEncryptionTypeUnset(g.GinkgoTB())
	})

	g.It("TestEncryptionTurnOnAndOff [Serial][Timeout:20m]", func() {
		TestEncryptionTurnOnAndOff(g.GinkgoTB())
	})
})

func TestEncryptionTypeIdentity(t testing.TB) {
	scenario := library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       operatorencryption.DefaultTargetGRs,
		AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
	}

	clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeIdentity, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
}

func TestEncryptionTypeUnset(t testing.TB) {
	scenario := library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       operatorencryption.DefaultTargetGRs,
		AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
	}

	clientSet := library.SetAndWaitForEncryptionType(t, "", scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
}

func TestEncryptionTurnOnAndOff(t testing.TB) {
	scenario := library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc:             operatorencryption.CreateAndStoreSecretOfLife,
		AssertResourceEncryptedFunc:    operatorencryption.AssertSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: operatorencryption.AssertSecretOfLifeNotEncrypted,
		ResourceFunc:                   operatorencryption.SecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider:             configv1.EncryptionType(*provider),
	}

	// Step 1: CreateAndStoreSecretOfLife
	t.Logf("CreateAndStore%s", scenario.ResourceName)
	scenario.CreateResourceFunc(t, library.GetClients(t), scenario.Namespace)

	// Step 2: Turn on encryption
	t.Logf("On%s", string(scenario.EncryptionProvider))
	testEncryptionType(t, scenario.BasicScenario, scenario.EncryptionProvider)

	// Step 3: Assert resource encrypted
	t.Logf("Assert%sEncrypted", scenario.ResourceName)
	resource := scenario.ResourceFunc(t, scenario.Namespace)
	scenario.AssertResourceEncryptedFunc(t, library.GetClients(t), resource)

	// Step 4: Turn off encryption (back to identity)
	t.Log("OffEncryption")
	testEncryptionType(t, scenario.BasicScenario, configv1.EncryptionTypeIdentity)

	// Step 5: Assert resource not encrypted
	t.Logf("Assert%sNotEncrypted", scenario.ResourceName)
	resource = scenario.ResourceFunc(t, scenario.Namespace)
	scenario.AssertResourceNotEncryptedFunc(t, library.GetClients(t), resource)
}

// testEncryptionType is a helper that replicates library.TestEncryptionType logic using testing.TB
func testEncryptionType(t testing.TB, scenario library.BasicScenario, provider configv1.EncryptionType) {
	switch provider {
	case configv1.EncryptionTypeAESCBC:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESCBC, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESCBC, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeAESGCM:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESGCM, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESGCM, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeIdentity, "":
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeIdentity, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
	default:
		t.Errorf("Unknown encryption type: %s", provider)
		t.FailNow()
	}
}
