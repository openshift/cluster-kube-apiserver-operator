package e2e_encryption_rotation

import (
	"context"
	"flag"
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	library "github.com/openshift/library-go/test/library/encryption"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var provider = flag.String("provider", "aescbc", "encryption provider used by the tests")

// TestEncryptionRotation first encrypts data then it forces a key
// rotation by setting the "encyrption.Reason" in the operator's configuration
// file
// Tags: Serial
// Timeout: 120m
func TestEncryptionRotation(t *testing.T) {
	library.TestEncryptionRotation(t, library.RotationScenario{
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
		EncryptionProvider: configv1.EncryptionType(*provider),
	})
}
