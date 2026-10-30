package e2e_encryption_rotation

import (
	"context"
	"fmt"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorlibrary "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestEncryptionRotation [Serial][Timeout:240m][Suite:encryption-rotation]", func(ctx context.Context) {
		testEncryptionRotation(ctx, g.GinkgoTB())
	})
})

// testEncryptionRotation first encrypts data then it forces a key
// rotation by setting the "encyrption.Reason" in the operator's configuration
// file
func testEncryptionRotation(ctx context.Context, t testing.TB) {
	encType := operatorencryption.EncryptionTypeFromEnv(t)
	t.Logf("encryption type: %s\n", encType)

	updateUnsupportedConfig := func(raw []byte) error {
		operatorClient := operatorencryption.GetOperator(t)
		apiServerOperator, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}
		apiServerOperator.Spec.UnsupportedConfigOverrides.Raw = raw
		_, err = operatorClient.Update(context.TODO(), apiServerOperator, metav1.UpdateOptions{})
		return err
	}

	library.TestEncryptionRotation(ctx, t, library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		CreateResourceFunc:          library.CreateAndStoreWellKnownSecretOfLife,
		GetRawResourceFunc:          library.GetRawWellKnownSecretOfLife,
		ForceRotationFunc:           library.StaticEncryptionForceRotation(updateUnsupportedConfig),
		WaitForRotationCompleteFunc: library.WaitForNextEncryptionKeyRotation(),
		EncryptionProvider:          library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: encType}},
	})

	// The dependent operators (oauth-apiserver, openshift-apiserver) roll out
	// asynchronously after the final revert-to-identity step. Wait for them to
	// settle so the post-test artifact gathering runs against a stable cluster,
	// otherwise the job fails in its post phase even though this test passed
	// (must-gather cannot create pods while the API plane is still rolling out,
	// which is guaranteed to happen on a single-node cluster).
	operatorencryption.WaitForDependentEncryptionOperatorsStable(ctx, t, 20*time.Minute)

	// The post-phase gathering also needs the default service account to be
	// provisioned in the fresh namespaces must-gather creates, otherwise its
	// pods are rejected with 'serviceaccount "default" not found'. That service
	// account is created by the service-account controller of
	// kube-controller-manager, which can still be crash-looping or catching up
	// from the API server rollouts above when the operators already report
	// stable (its ClusterOperator keeps reporting Available in that state, so
	// it cannot be detected via operator conditions).
	waitForDefaultServiceAccountProvisioning(ctx, t, 15*time.Minute)
}

// waitForDefaultServiceAccountProvisioning waits until a freshly created
// namespace receives its default service account, i.e. until the controllers
// provisioning it (primarily the service-account controller of
// kube-controller-manager) service new namespaces again. It creates a probe
// namespace for that and removes it again once the service account showed up.
func waitForDefaultServiceAccountProvisioning(ctx context.Context, t testing.TB, timeout time.Duration) {
	t.Helper()

	kubeConfig, err := operatorlibrary.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	require.NoError(t, err)

	probeNamespace, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "encryption-e2e-sa-probe-"},
	}, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create the service account probe namespace")
	defer func() {
		if err := kubeClient.CoreV1().Namespaces().Delete(context.Background(), probeNamespace.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("failed to delete the service account probe namespace %s: %v", probeNamespace.Name, err)
		}
	}()

	serviceAccounts := kubeClient.CoreV1().ServiceAccounts(probeNamespace.Name)
	t.Logf("Waiting up to %s for the default service account to be provisioned in the probe namespace %s", timeout, probeNamespace.Name)
	require.Eventually(t, func() bool {
		_, err := serviceAccounts.Get(ctx, "default", metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("unable to get the default service account: %v, will retry", err)
		}
		return err == nil
	}, timeout, 2*time.Second,
		"the default service account was not provisioned in the probe namespace %s within %s (the kube-controller-manager service-account controller is not servicing new namespaces, the post-phase must-gather would fail)",
		probeNamespace.Name, timeout)
}
