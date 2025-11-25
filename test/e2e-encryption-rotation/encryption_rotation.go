package e2e_encryption_rotation

import (
	"context"
	"flag"
	"fmt"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
)

var providerRotation = flag.String("provider-rotation", "aescbc", "encryption provider used by the rotation tests")

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator encryption rotation", func() {
	g.It("TestEncryptionRotation [Serial][Timeout:30m]", func() {
		TestEncryptionRotation(g.GinkgoTB())
	})
})

// TestEncryptionRotation first encrypts data then it forces a key
// rotation by setting the "encyrption.Reason" in the operator's configuration
// file
func TestEncryptionRotation(t testing.TB) {
	scenario := library.RotationScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		CreateResourceFunc: operatorencryption.CreateAndStoreSecretOfLife,
		GetRawResourceFunc: operatorencryption.GetRawSecretOfLife,
		UnsupportedConfigFunc: func(raw []byte) error {
			operatorClient := operatorencryption.GetOperator(t)
			apiServerOperator, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return err
			}
			apiServerOperator.Spec.UnsupportedConfigOverrides.Raw = raw
			_, err = operatorClient.Update(context.TODO(), apiServerOperator, metav1.UpdateOptions{})
			return err
		},
		EncryptionProvider: configv1.EncryptionType(*providerRotation),
	}

	// Replicate the logic from library.TestEncryptionRotation but using testing.TB

	// step 1: create the desired resource
	clientSet := library.GetClients(t)
	scenario.CreateResourceFunc(t, library.GetClients(t), scenario.Namespace)

	// step 2: run provided encryption scenario
	testEncryptionType(t, scenario.BasicScenario, scenario.EncryptionProvider)

	// step 3: take samples
	rawEncryptedResourceWithKey1 := scenario.GetRawResourceFunc(t, clientSet, scenario.Namespace)

	// step 4: force key rotation and wait for migration to complete
	lastMigratedKeyMeta, err := library.GetLastKeyMeta(t, clientSet.Kube, scenario.Namespace, scenario.LabelSelector)
	require.NoError(t, err)
	require.NoError(t, library.ForceKeyRotation(t, scenario.UnsupportedConfigFunc, fmt.Sprintf("test-key-rotation-%s", rand.String(4))))
	library.WaitForNextMigratedKey(t, clientSet.Kube, lastMigratedKeyMeta, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(t, clientSet, scenario.EncryptionProvider, scenario.Namespace, scenario.LabelSelector)

	// step 5: verify if the provided resource was encrypted with a different key (step 2 vs step 4)
	rawEncryptedResourceWithKey2 := scenario.GetRawResourceFunc(t, clientSet, scenario.Namespace)
	if rawEncryptedResourceWithKey1 == rawEncryptedResourceWithKey2 {
		t.Errorf("expected the resource to has a different content after a key rotation,\ncontentBeforeRotation %s\ncontentAfterRotation %s", rawEncryptedResourceWithKey1, rawEncryptedResourceWithKey2)
	}
}

// testEncryptionType is a helper that replicates library.TestEncryptionType logic using testing.TB
func testEncryptionType(t testing.TB, scenario library.BasicScenario, provider configv1.EncryptionType) {
	switch provider {
	case configv1.EncryptionTypeAESCBC:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESCBC, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESCBC, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeAESGCM:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESGCM, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESGCM, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeIdentity, "":
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeIdentity, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
	default:
		t.Errorf("Unknown encryption type: %s", provider)
		t.FailNow()
	}
}
