package encryptionstatusprovider

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorv1typed "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"

	"github.com/openshift/library-go/pkg/operator/encryption/kms"
)

// NewKubeAPIServerEncryptionStatusProvider builds a kms.EncryptionStatusProvider for
// KubeAPIServer/cluster from an operator client.
func NewKubeAPIServerEncryptionStatusProvider(client operatorclient.Interface) (kms.EncryptionStatusProvider, error) {
	return &kubeAPIServerEncryptionStatusProvider{client: client.OperatorV1().KubeAPIServers()}, nil
}

var _ kms.EncryptionStatusProvider = &kubeAPIServerEncryptionStatusProvider{}

type kubeAPIServerEncryptionStatusProvider struct {
	client operatorv1typed.KubeAPIServerInterface
}

func (p *kubeAPIServerEncryptionStatusProvider) GetKMSEncryptionStatus(ctx context.Context) (*operatorv1.KMSEncryptionStatus, error) {
	obj, err := p.client.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &obj.Status.EncryptionStatus, nil
}

func (p *kubeAPIServerEncryptionStatusProvider) ApplyKMSEncryptionStatus(ctx context.Context, fieldManager string, status *applyoperatorv1.KMSEncryptionStatusApplyConfiguration) error {
	_, err := p.client.ApplyStatus(
		ctx,
		applyoperatorv1.KubeAPIServer("cluster").WithStatus(applyoperatorv1.KubeAPIServerStatus().WithEncryptionStatus(status)),
		metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	return err
}

func (p *kubeAPIServerEncryptionStatusProvider) UpdateKMSEncryptionStatus(ctx context.Context, mutateFn func(*operatorv1.KMSEncryptionStatus)) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		obj, err := p.client.Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}
		mutateFn(&obj.Status.EncryptionStatus)
		_, err = p.client.UpdateStatus(ctx, obj, metav1.UpdateOptions{})
		return err
	})
}
