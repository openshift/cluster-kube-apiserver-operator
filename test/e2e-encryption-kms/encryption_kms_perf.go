package e2e_encryption_kms

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSPerfEncryption [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-perf]", func(ctx context.Context) {
		testKMSPerfEncryption(ctx, g.GinkgoTB())
	})
})

// testKMSPerfEncryption measures Vault KMS encryption migration time across
// kube-apiserver, oauth-apiserver, and openshift-apiserver.
//
// DB load thresholds (25000 secrets / 14000 configmaps) match the AES perf
// suite: 3 workers × (9010 secrets / 5010 configmaps) with slack for create
// failures during the load storm.
func testKMSPerfEncryption(ctx context.Context, t testing.TB) {
	library.TestKMSPerfEncryption(ctx, t, librarykms.EncryptionPerfScenarios(ctx, t, librarykms.EncryptionPerfConfig{
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
	})...)
}
