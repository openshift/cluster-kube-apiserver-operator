package e2e_encryption_perf

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"

	g "github.com/onsi/ginkgo/v2"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestPerfEncryption [Serial][Timeout:120m][Suite:encryption-perf]", func(ctx context.Context) {
		testPerfEncryption(ctx, g.GinkgoTB())
	})
})

func testPerfEncryption(ctx context.Context, tt testing.TB) {
	encType := operatorencryption.EncryptionTypeFromEnv(tt)
	tt.Logf("encryption type: %s\n", encType)
	operatorClient := operatorencryption.GetOperator(tt)
	library.TestPerfEncryption(ctx, tt, library.PerfScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		GetOperatorConditionsFunc: func(t testing.TB) ([]operatorv1.OperatorCondition, error) {
			apiServerOperator, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return apiServerOperator.Status.Conditions, nil
		},
		DBLoaderWorkers:       operatorencryption.DefaultDBLoaderWorkers,
		DBLoaderFunc:          operatorencryption.DefaultDBLoader(),
		AssertDBPopulatedFunc: operatorencryption.AssertDBPopulated,
		AssertMigrationTime:   operatorencryption.AssertMigrationTime,
		EncryptionProvider:    library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: encType}},
	})
}
