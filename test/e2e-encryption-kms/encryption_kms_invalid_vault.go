package e2e_encryption_kms

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	testlibrary "github.com/openshift/library-go/test/library"
	library "github.com/openshift/library-go/test/library/encryption"
	librarykms "github.com/openshift/library-go/test/library/encryption/kms"
	operatorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

// invalidVaultAddress is an RFC 5737 TEST-NET address that is guaranteed to be unreachable
// in any real network. Using it as the Vault address causes KMS plugin connection attempts
// to time out, which the preflight controller interprets as a configuration failure.
const invalidVaultAddress = "https://192.0.2.1:8200"

const (
	encryptionKMSPreflightControllerDegraded = "EncryptionKMSPreflightControllerDegraded"
	kasEncryptionNamespace                   = operatorclient.GlobalMachineSpecifiedConfigNamespace
	kasEncryptionComponent                   = operatorclient.TargetNamespace

	waitForPreflightDegradedTimeout         = 30 * time.Minute
	waitForPreflightRecoveredTimeout         = 30 * time.Minute
	consistentlyNoDegradedMigrationDuration = 3 * time.Minute
	preflightConditionPollInterval          = 15 * time.Second
)

func kasEncryptionLabelSelector() string {
	return "encryption.apiserver.operator.openshift.io/component=" + kasEncryptionComponent
}

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestKMSEncryptionInvalidVaultRecovery [OCPFeatureGate:KMSEncryption][Serial][Timeout:120m][Suite:encryption-kms-2]", func(ctx context.Context) {
		testKMSEncryptionInvalidVaultRecovery(ctx, g.GinkgoTB())
	})
})

// testKMSEncryptionInvalidVaultRecovery validates that the encryption migration lifecycle
// respects preflight gating when the initial Vault configuration is invalid.
//
// Sequence:
//
//	Invalid Vault address
//	        ↓
//	Preflight = Degraded
//	        ↓
//	Migration = NOT started  (invariant: "Preflight failure must prevent migration")
//	        ↓
//	Fix Vault address
//	        ↓
//	Preflight = Succeeded
//	        ↓
//	Migration = Started → Completed
func testKMSEncryptionInvalidVaultRecovery(ctx context.Context, t testing.TB) {
	t.Helper()

	cs := library.GetClients(t)
	operatorClient := newKubeAPIServerOperatorClient(t)

	// Build the valid provider (resolves Vault Service ClusterIP, ensures AppRole secret exists).
	validProvider := librarykms.DefaultVaultEncryptionProvider(ctx, t)
	validProvider.Setup(ctx, t)

	// Derive the invalid provider: identical to the valid one except the Vault address,
	// which is replaced with an unreachable TEST-NET address.  All credentials, TLS
	// config, and key paths remain valid so the only failure mode is the bad address.
	invalidCfg := validProvider.APIServerEncryption
	invalidCfg.KMS.Vault.VaultAddress = invalidVaultAddress

	// Snapshot the current encryption key state so we can detect any spurious key creation.
	keyMetaBefore, err := library.GetLastKeyMeta(t, cs.Kube, kasEncryptionNamespace, kasEncryptionLabelSelector())
	require.NoError(t, err, "snapshot encryption key state before test")
	t.Logf("Baseline encryption state: key=%q mode=%q migrated=%v",
		keyMetaBefore.Name, keyMetaBefore.Mode, keyMetaBefore.Migrated)

	// Cleanup: always restore the valid Vault address, even on test failure, so that
	// subsequent tests are not blocked by a broken encryption configuration.
	t.Cleanup(func() {
		fixAPIServerEncryption(context.Background(), t, cs, validProvider.APIServerEncryption)
	})

	// ── Step 1: apply the invalid Vault address ───────────────────────────────────────────
	t.Logf("Step 1: applying KMS encryption config with invalid Vault address %q", invalidVaultAddress)
	applyAPIServerEncryption(ctx, t, cs, invalidCfg)

	// ── Step 2: wait for preflight to report Degraded ────────────────────────────────────
	t.Logf("Step 2: waiting up to %s for %s=True", waitForPreflightDegradedTimeout, encryptionKMSPreflightControllerDegraded)
	degradedCond := waitForPreflightConditionDegraded(ctx, t, operatorClient)
	t.Logf("Preflight reported Degraded — reason=%q message=%q", degradedCond.Reason, degradedCond.Message)

	// ── Step 3: verify no migration started while preflight is degraded ──────────────────
	//
	// Two-phase check:
	//   a) Immediate: assert the key name has not changed since the baseline snapshot.
	//   b) Consistent: poll for consistentlyNoDegradedMigrationDuration and fail if any
	//      new key appears. This proves the preflight gate is actively blocking key
	//      creation, not just that the key-controller hasn't woken up yet.
	t.Logf("Step 3: asserting no migration started while preflight is degraded")

	keyMetaAtDegraded, err := library.GetLastKeyMeta(t, cs.Kube, kasEncryptionNamespace, kasEncryptionLabelSelector())
	require.NoError(t, err, "read encryption key state after Degraded confirmed")
	require.Equal(t, keyMetaBefore.Name, keyMetaAtDegraded.Name,
		"Migration must not start while encryption preflight is degraded: "+
			"expected key %q, got %q", keyMetaBefore.Name, keyMetaAtDegraded.Name)

	assertConsistentlyNoNewKey(ctx, t, cs, kasEncryptionNamespace, kasEncryptionLabelSelector(), keyMetaBefore)

	// ── Step 4: fix the Vault address ────────────────────────────────────────────────────
	t.Logf("Step 4: updating APIServer encryption to valid Vault address")
	applyAPIServerEncryption(ctx, t, cs, validProvider.APIServerEncryption)

	// ── Step 5: wait for preflight to recover ────────────────────────────────────────────
	t.Logf("Step 5: waiting up to %s for preflight to recover", waitForPreflightRecoveredTimeout)
	waitForPreflightConditionRecovered(ctx, t, operatorClient)
	t.Logf("Preflight recovered: %s=False and result.status=Succeeded", encryptionKMSPreflightControllerDegraded)

	// ── Steps 6 + 7: verify migration initiates and completes ────────────────────────────
	//
	// The baseline key meta (keyMetaBefore) was captured before the invalid config was
	// applied. WaitForNextMigratedKey waits for a key newer than that baseline to finish
	// migration.  Reaching this line at all proves migration was NOT initiated while
	// preflight was degraded — any key creation during that window would have failed the
	// consistent check above.
	t.Logf("Step 6/7: waiting for encryption migration to initiate and complete (baseline key=%q)", keyMetaBefore.Name)
	library.WaitForNextMigratedKey(t, cs.Kube, keyMetaBefore,
		library.WellKnownKASTargetGRs,
		kasEncryptionNamespace,
		kasEncryptionLabelSelector())
	t.Logf("Migration completed successfully")

	// ── Final: assert no Degraded conditions remain ───────────────────────────────────────
	assertOperatorNotDegraded(ctx, t, operatorClient)
}

// applyAPIServerEncryption sets APIServer.spec.encryption, retrying on conflicts.
func applyAPIServerEncryption(ctx context.Context, t testing.TB, cs library.ClientSet, encryption configv1.APIServerEncryption) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		apiServer, getErr := cs.ApiServerConfig.Get(ctx, "cluster", metav1.GetOptions{})
		if getErr != nil {
			t.Logf("get APIServer: %v", getErr)
			return false, nil
		}
		apiServer.Spec.Encryption = encryption
		_, updateErr := cs.ApiServerConfig.Update(ctx, apiServer, metav1.UpdateOptions{})
		if apierrors.IsConflict(updateErr) {
			return false, nil // retry on conflict
		}
		if updateErr != nil {
			t.Logf("update APIServer encryption: %v", updateErr)
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err, "apply APIServer encryption config")
}

// fixAPIServerEncryption is a best-effort version of applyAPIServerEncryption intended for
// use in t.Cleanup.  It silently absorbs errors so it does not mask the original test failure.
func fixAPIServerEncryption(ctx context.Context, t testing.TB, cs library.ClientSet, encryption configv1.APIServerEncryption) {
	t.Helper()
	_ = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		apiServer, err := cs.ApiServerConfig.Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		apiServer.Spec.Encryption = encryption
		_, err = cs.ApiServerConfig.Update(ctx, apiServer, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			return false, nil
		}
		return err == nil, nil
	})
}

// waitForPreflightConditionDegraded polls KubeAPIServer/cluster until
// EncryptionKMSPreflightControllerDegraded=True and returns the condition for logging.
func waitForPreflightConditionDegraded(ctx context.Context, t testing.TB, operatorClient operatorclientset.Interface) operatorv1.OperatorCondition {
	t.Helper()
	var degraded operatorv1.OperatorCondition
	err := wait.PollUntilContextTimeout(ctx, preflightConditionPollInterval, waitForPreflightDegradedTimeout, true,
		func(ctx context.Context) (bool, error) {
			kas, getErr := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			if getErr != nil {
				t.Logf("get KubeAPIServer: %v", getErr)
				return false, nil
			}
			cond := operatorhelpers.FindOperatorCondition(kas.Status.Conditions, encryptionKMSPreflightControllerDegraded)
			if cond == nil {
				t.Logf("%s condition not yet present", encryptionKMSPreflightControllerDegraded)
				return false, nil
			}
			if cond.Status != operatorv1.ConditionTrue {
				t.Logf("%s=%s (waiting for True)", encryptionKMSPreflightControllerDegraded, cond.Status)
				return false, nil
			}
			degraded = *cond
			return true, nil
		})
	require.NoError(t, err, "timed out waiting for %s=True", encryptionKMSPreflightControllerDegraded)
	return degraded
}

// waitForPreflightConditionRecovered polls KubeAPIServer/cluster until both:
//   - EncryptionKMSPreflightControllerDegraded is absent or False, AND
//   - EncryptionStatus.Preflight.Result.Status = Succeeded with a matching config hash.
func waitForPreflightConditionRecovered(ctx context.Context, t testing.TB, operatorClient operatorclientset.Interface) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, preflightConditionPollInterval, waitForPreflightRecoveredTimeout, true,
		func(ctx context.Context) (bool, error) {
			kas, getErr := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
			if getErr != nil {
				t.Logf("get KubeAPIServer: %v", getErr)
				return false, nil
			}

			// Condition must be absent or False.
			if operatorhelpers.IsOperatorConditionPresentAndEqual(
				kas.Status.Conditions,
				encryptionKMSPreflightControllerDegraded,
				operatorv1.ConditionTrue,
			) {
				cond := operatorhelpers.FindOperatorCondition(kas.Status.Conditions, encryptionKMSPreflightControllerDegraded)
				t.Logf("%s still True — reason=%q message=%q", encryptionKMSPreflightControllerDegraded, cond.Reason, cond.Message)
				return false, nil
			}

			// Preflight result must be Succeeded and the hash must match what was observed.
			pf := kas.Status.EncryptionStatus.Preflight
			if pf.Result.Status != operatorv1.KMSPreflightResultSucceeded {
				t.Logf("preflight result not yet Succeeded: status=%q configHash=%q observedConfigHash=%q",
					pf.Result.Status, pf.Result.ConfigHash, pf.ObservedConfigHash)
				return false, nil
			}
			if pf.Result.ConfigHash != pf.ObservedConfigHash {
				t.Logf("preflight result hash mismatch: result.configHash=%q observedConfigHash=%q",
					pf.Result.ConfigHash, pf.ObservedConfigHash)
				return false, nil
			}

			return true, nil
		})
	require.NoError(t, err, "timed out waiting for preflight to recover after Vault address fix")
}

// assertConsistentlyNoNewKey polls for consistentlyNoDegradedMigrationDuration and fails
// immediately if any new encryption key appears.  Calling this right after confirming
// the Degraded condition proves the preflight gate actively blocks key creation.
func assertConsistentlyNoNewKey(
	ctx context.Context,
	t testing.TB,
	cs library.ClientSet,
	namespace, labelSelector string,
	keyMetaBefore library.EncryptionKeyMeta,
) {
	t.Helper()
	t.Logf("Consistently asserting no new encryption key for %s (baseline key=%q)",
		consistentlyNoDegradedMigrationDuration, keyMetaBefore.Name)

	deadline := time.Now().Add(consistentlyNoDegradedMigrationDuration)
	err := wait.PollUntilContextTimeout(
		ctx,
		preflightConditionPollInterval,
		consistentlyNoDegradedMigrationDuration+preflightConditionPollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			current, getErr := library.GetLastKeyMeta(t, cs.Kube, namespace, labelSelector)
			if getErr != nil {
				t.Logf("get key meta: %v", getErr)
				return false, nil
			}
			if current.Name != keyMetaBefore.Name {
				return false, fmt.Errorf(
					"Migration must not start while encryption preflight is degraded: "+
						"new key %q appeared while Degraded (baseline key: %q)",
					current.Name, keyMetaBefore.Name)
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				t.Logf("Consistently confirmed: no new encryption key created during %s while preflight was Degraded",
					consistentlyNoDegradedMigrationDuration)
				return true, nil
			}
			t.Logf("Consistent check: key still %q, %s remaining",
				current.Name, remaining.Round(time.Second))
			return false, nil
		},
	)
	require.NoError(t, err, "assertConsistentlyNoNewKey: migration invariant violated while preflight was Degraded")
}

// assertOperatorNotDegraded checks that no *Degraded condition is True after migration
// completes.  It collects all violations before failing so the log shows the full picture.
func assertOperatorNotDegraded(ctx context.Context, t testing.TB, operatorClient operatorclientset.Interface) {
	t.Helper()
	kas, err := operatorClient.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	require.NoError(t, err, "get KubeAPIServer for final health check")

	var violations []string
	for _, cond := range kas.Status.Conditions {
		if strings.HasSuffix(string(cond.Type), "Degraded") && cond.Status == operatorv1.ConditionTrue {
			violations = append(violations,
				fmt.Sprintf("  type=%q reason=%q message=%q", cond.Type, cond.Reason, cond.Message))
		}
	}
	require.Empty(t, violations,
		"unexpected Degraded condition(s) after migration completed:\n%s",
		strings.Join(violations, "\n"))
}

// newKubeAPIServerOperatorClient builds a typed operator/v1 client using the in-cluster or
// KUBECONFIG credentials, following the same pattern as other e2e helpers.
func newKubeAPIServerOperatorClient(t testing.TB) operatorclientset.Interface {
	t.Helper()
	kubeConfig, err := testlibrary.NewClientConfigForTest()
	require.NoError(t, err, "build kubeconfig for operator client")
	return operatorclientset.NewForConfigOrDie(kubeConfig)
}
