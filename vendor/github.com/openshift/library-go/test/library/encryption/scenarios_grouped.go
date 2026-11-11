package encryption

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

// TestKMSLifecycleGroup runs negatives → valid KMS → in-place updates → remote-key
// rotation, sharing valid KMS state across the later phases.
//
// Successful runs leave KMS enabled: OnOff owns KMS↔identity coverage, and CI
// clusters are disposable. Unlike the standalone runners, this group does not
// reset to identity between updates and rotation, and it does not re-enable KMS
// before rotation. On failure during the negative-preflight phase it best-effort
// restores identity so a broken fixture is not left behind; after valid KMS is
// reached, failure leaves the cluster as-is.
func TestKMSLifecycleGroup(ctx context.Context, t testing.TB, lifecycle []KMSPreflightLifecycleScenario, rotation []RotationScenario) {
	t.Helper()
	require.NotEmpty(t, lifecycle, "TestKMSLifecycleGroup requires lifecycle scenarios")
	require.NotEmpty(t, rotation, "TestKMSLifecycleGroup requires rotation scenarios")

	e := NewE(t, PrintEventsOnFailure(lifecycle[0].OperatorNamespace))
	requireEncryptionOff(ctx, e)

	var recoverIdentity atomic.Bool
	recoverIdentity.Store(true)
	defer func() {
		if !t.Failed() || !recoverIdentity.Load() {
			return
		}
		t.Logf("lifecycle group failed before valid KMS; restoring identity")
		restoreIdentityBestEffort(context.Background(), e, lifecycle[0].BasicScenario)
	}()

	runTestSteps(e, kmsLifecycleGroupTestSteps(ctx, t, lifecycle, rotation, &recoverIdentity))
}

// TestKMSMigrationGroup runs manual preflight deploy (with phase-scoped cleanup),
// then KMS↔KMS provider migration. Successful runs leave the final KMS provider
// enabled; OnOff owns identity coverage. Standalone in-place update coverage
// belongs in TestKMSLifecycleGroup, not here.
func TestKMSMigrationGroup(ctx context.Context, t testing.TB, deploy PreflightDeployScenario, migration ...ProvidersMigrationScenario) {
	t.Helper()
	require.NotEmpty(t, migration, "TestKMSMigrationGroup requires migration scenarios")

	runTestSteps(t, []testStep{
		{name: "PreflightDeploy", testFunc: func(t testing.TB) {
			TestPreflightDeployAndPodMatchesOperand(ctx, t, deploy)
		}},
		{name: "KMSToKMSMigration", testFunc: func(t testing.TB) {
			runTestSteps(t, providersMigrationTestSteps(ctx, t, providersMigrationOptions{includeFinalIdentity: false}, migration...))
		}},
	})
}

// kmsLifecycleGroupTestSteps builds the grouped lifecycle+rotation step list.
// recoverIdentity may be nil for unit tests that only inspect step names.
func kmsLifecycleGroupTestSteps(ctx context.Context, t testing.TB, lifecycle []KMSPreflightLifecycleScenario, rotation []RotationScenario, recoverIdentity *atomic.Bool) []testStep {
	t.Helper()
	afterValidKMS := []testStep{{
		name: "MarkValidKMSReached",
		testFunc: func(testing.TB) {
			if recoverIdentity != nil {
				recoverIdentity.Store(false)
			}
		},
	}}
	lifecycleSteps, _ := kmsPreflightLifecycleTestSteps(ctx, t, kmsPreflightLifecycleOptions{
		includeFinalIdentity: false,
		afterValidKMS:        afterValidKMS,
	}, lifecycle...)
	rotationSteps := rotationTestSteps(ctx, t, rotationStepOptions{
		includeCreateEncrypt: false,
		includeFinalIdentity: false,
	}, rotation...)
	steps := append([]testStep{}, lifecycleSteps...)
	return append(steps, rotationSteps...)
}

func restoreIdentityBestEffort(ctx context.Context, t testing.TB, scenario BasicScenario) {
	t.Helper()
	clientSet := GetClients(t)
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic restoring identity: %v", r)
			}
		}()
		apiServer, getErr := clientSet.ApiServerConfig.Get(ctx, "cluster", metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if apiServer.Spec.Encryption.Type == "" || apiServer.Spec.Encryption.Type == configv1.EncryptionTypeIdentity {
			return nil
		}
		TestEncryptionTypeIdentity(ctx, t, scenario)
		return nil
	}()
	if err != nil {
		t.Logf("best-effort identity restore failed: %v", err)
	}
}
