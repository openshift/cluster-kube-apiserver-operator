package encryptionstatusprovider

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"

	"github.com/openshift/library-go/pkg/operator/encryption/kms"
)

// NewKubeAPIServerEncryptionStatusProviderFromConfig builds a kms.EncryptionStatusProvider for
// KubeAPIServer/cluster from a rest.Config.
func NewKubeAPIServerEncryptionStatusProviderFromConfig(restConfig *rest.Config) (kms.EncryptionStatusProvider, error) {
	client, err := operatorclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build operator client: %w", err)
	}
	return &kubeAPIServerEncryptionStatusProvider{client: client}, nil
}

var _ kms.EncryptionStatusProvider = &kubeAPIServerEncryptionStatusProvider{}

type kubeAPIServerEncryptionStatusProvider struct {
	client *operatorclient.Clientset
}

func (p *kubeAPIServerEncryptionStatusProvider) GetKMSEncryptionStatus(ctx context.Context) (*operatorv1.KMSEncryptionStatus, error) {
	obj, err := p.client.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &obj.Status.EncryptionStatus, nil
}

func (p *kubeAPIServerEncryptionStatusProvider) ApplyKMSEncryptionStatus(ctx context.Context, fieldManager string, status *applyoperatorv1.KMSEncryptionStatusApplyConfiguration) error {
	_, err := p.client.OperatorV1().KubeAPIServers().ApplyStatus(
		ctx,
		applyoperatorv1.KubeAPIServer("cluster").WithStatus(applyoperatorv1.KubeAPIServerStatus().WithEncryptionStatus(status)),
		metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	return err
}

func (p *kubeAPIServerEncryptionStatusProvider) UpdateKMSEncryptionStatus(ctx context.Context, mutateFn func(*operatorv1.KMSEncryptionStatus)) error {
	obj, err := p.client.OperatorV1().KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}
	mutateFn(&obj.Status.EncryptionStatus)
	_, err = p.client.OperatorV1().KubeAPIServers().UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	return err
}
