package e2e_encryption_perf

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		AssertDBPopulatedFunc: func(t testing.TB, errorStore map[string]int, statStore map[string]int) {
			secretsCount, ok := statStore[operatorencryption.SecretsStatsKey]
			if !ok {
				err := errors.New("missing secrets count stats, can't continue the test")
				require.NoError(t, err)
			}
			if secretsCount < 25000 {
				err := fmt.Errorf("expected to create at least 25000 secrets but %d were created", secretsCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d secrets", secretsCount)

			configMapsCount, ok := statStore[operatorencryption.CMStatsKey]
			if !ok {
				err := errors.New("missing configmaps count stats, can't continue the test")
				require.NoError(t, err)
			}
			if configMapsCount < 14000 {
				err := fmt.Errorf("expected to create at least 14000 configmaps but %d were created", configMapsCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d configmaps", configMapsCount)
		},
		AssertMigrationTime: func(t testing.TB, migrationTime time.Duration) {
			t.Logf("migration took %v", migrationTime)
			expectedMigrationTime := 28 * time.Minute
			if migrationTime > expectedMigrationTime {
				t.Errorf("migration took too long (%v), expected it to take no more than %v", migrationTime, expectedMigrationTime)
			}
		},
		DBLoaderWorkers: 3,
		DBLoaderFunc: library.DBLoaderRepeat(1, true,
			operatorencryption.CreateNamespace,
			operatorencryption.WaitUntilNamespaceActive,
			library.DBLoaderRepeatParallel(5010, 50, false, operatorencryption.CreateConfigMap, operatorencryption.ReportConfigMap),
			library.DBLoaderRepeatParallel(9010, 50, false, operatorencryption.CreateSecret, operatorencryption.ReportSecret)),
		EncryptionProvider: library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: encType}},
	})
}
