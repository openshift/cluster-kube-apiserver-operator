package e2e

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
)

func TestEncryptionTypeAESCBC(t *testing.T) {
	library.TestEncryptionTypeAESCBC(t, library.BasicScenario{
		Namespace:     operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector: "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		TargetGRs:     operatorencryption.DefaultTargetGRs,
		AssertFunc:    operatorencryption.AssertSecretsAndConfigMaps,
	})
}
