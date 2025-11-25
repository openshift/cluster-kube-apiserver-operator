package e2e

import (
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestEncryptionTypeAESCBC [Serial]", func() {
		TestEncryptionTypeAESCBC(g.GinkgoTB())
	})
})

func TestEncryptionTypeAESCBC(t testing.TB) {
	// Replicate the logic from library.TestEncryptionTypeAESCBC
	// but using testing.TB instead of *testing.T
	scenario := library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       operatorencryption.DefaultTargetGRs,
		AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
	}

	// Call the library functions directly with testing.TB
	clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESCBC, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESCBC, scenario.Namespace, scenario.LabelSelector)
	library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
}
