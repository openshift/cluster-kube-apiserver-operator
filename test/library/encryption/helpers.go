package encryption

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func GetOperator(t testing.TB) operatorv1client.KubeAPIServerInterface {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return operatorClient.KubeAPIServers()
}

// EncryptionTypeFromEnv returns the encryption type from ENCRYPTION_PROVIDER
// (set by Makefile or OTE/CI). This is the single configuration path for e2e
// encryption provider selection.
func EncryptionTypeFromEnv(t testing.TB) configv1.EncryptionType {
	t.Helper()
	env := os.Getenv("ENCRYPTION_PROVIDER")
	if env == "" {
		t.Fatal("ENCRYPTION_PROVIDER env var is required (e.g. aescbc, aesgcm)")
	}
	return configv1.EncryptionType(env)
}

// encryptionOperators lists the cluster operators that roll out whenever an
// encryption e2e test changes the cluster-wide encryption configuration from
// apiservers.config.openshift.io: kube-apiserver itself (every encryption-config
// change forces a new static pod revision and a master-by-master
// NodeInstaller roll) plus the operators that mirror the configuration
// (oauth-apiserver behind the authentication operator and openshift-apiserver).
var encryptionOperators = []string{"kube-apiserver", "authentication", "openshift-apiserver"}

// minStableFor is how long the encryption operators must continuously report
// settled conditions before WaitForEncryptionOperatorsStable returns.
// A single healthy poll is not accepted: an operator that has not observed the
// latest encryption change yet still shows its pre-rollout conditions, and a
// graceful rollout can keep Available=True and Degraded=False while it is in
// flight. Requiring the conditions to hold for a window catches a rollout that
// starts late, because it breaks the window and restarts the wait. This
// includes the deferred read-key pruning roll (see
// WaitForEncryptionOperatorsStable), which starts about a minute after the
// final key migration is reported complete.
const minStableFor = 2 * time.Minute

// WaitForEncryptionOperatorsStable waits until every operator that rolls out
// for a cluster-wide encryption change (kube-apiserver, oauth-apiserver behind
// the authentication operator, and openshift-apiserver) finishes its rollout
// and continuously reports Available, not Degraded and not Progressing for at
// least minStableFor.
//
// The encryption e2e tests consider a transition done when the target
// encryption key secret is annotated as fully migrated, but that is not the
// end of the operator's state machine: right after the migration completes,
// the encryption controllers drop the read key that is no longer needed,
// which rewrites the encryption-config secret and forces one more
// kube-apiserver static pod revision plus a full master-by-master
// NodeInstaller roll. No key-migration wait covers this final roll, so the
// suite can exit while kube-apiserver is still Progressing. The post-suite
// e2e-analysis check (verify operator conditions kube-apiserver) snapshots
// the clusteroperators once and fails on that transient Progressing
// condition (see OCPBUGS-139148). On single-node clusters the post-test
// artifact gathering fails the same way: the API plane is intermittently
// unavailable and must-gather cannot create its pods (the default service
// account of the freshly created namespace does not exist yet), turning the
// job red even though the test itself passed. Waiting here leaves the cluster
// settled by the time the test exits.
func WaitForEncryptionOperatorsStable(ctx context.Context, t testing.TB, timeout time.Duration) {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)
	clusterOperators := configClient.ConfigV1().ClusterOperators()

	t.Logf("Waiting up to %s for the encryption operators %v to report Available, not Degraded and not Progressing for at least %s, interval = 20s", timeout, encryptionOperators, minStableFor)

	var lastErr error
	stableSince := time.Time{}
	err = wait.PollUntilContextTimeout(ctx, 20*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		settled := true
		for _, name := range encryptionOperators {
			co, err := clusterOperators.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				lastErr = fmt.Errorf("unable to get clusteroperator %s: %w", name, err)
				t.Logf("%v, will retry", lastErr)
				settled = false
				break
			}

			available := configv1helpers.IsStatusConditionTrue(co.Status.Conditions, configv1.OperatorAvailable)
			degraded := configv1helpers.IsStatusConditionTrue(co.Status.Conditions, configv1.OperatorDegraded)
			progressing := configv1helpers.IsStatusConditionTrue(co.Status.Conditions, configv1.OperatorProgressing)
			if !available || degraded || progressing {
				t.Logf("clusteroperator %s hasn't settled yet: available=%v degraded=%v progressing=%v", name, available, degraded, progressing)
				settled = false
			}
		}

		switch {
		case !settled:
			// an operator is still rolling (or unreachable), restart the stability window
			stableSince = time.Time{}
		case stableSince.IsZero():
			// all operators just settled, start the stability window
			stableSince = time.Now()
		}
		return !stableSince.IsZero() && time.Since(stableSince) >= minStableFor, nil /*done*/
	})
	require.NoError(t, err, "timed out waiting for the encryption operators to become stable, last error: %v", lastErr)
}
