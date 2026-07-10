package e2e

import (
	"context"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	library "github.com/openshift/library-go/test/library/encryption"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial][Timeout:40m] TestEncryptionTypeAESCBC", func(ctx context.Context) {
		testEncryptionTypeAESCBC(ctx, g.GinkgoTB())
	})
})

func testEncryptionTypeAESCBC(ctx context.Context, t testing.TB) {
	library.TestEncryptionTypeAESCBC(ctx, t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}
