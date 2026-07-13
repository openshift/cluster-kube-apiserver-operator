package encryptionstatusclient

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorv1typed "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"

	libencryptionstatus "github.com/openshift/library-go/pkg/operator/encryption/kms/encryptionstatus"
)

// NewKubeAPIServerClient returns a KMSEncryptionStatusClient that reads and
// writes KubeAPIServer/cluster at .status.encryptionStatus.
func NewKubeAPIServerClient(getter operatorv1typed.KubeAPIServersGetter) libencryptionstatus.KMSEncryptionStatusClient {
	return &kubeAPIServerClient{getter: getter}
}

// NewKubeAPIServerClientFromConfig builds a KMSEncryptionStatusClient for
// KubeAPIServer/cluster from a rest.Config. Used by sidecar binaries that
// defer REST client creation until startup when the in-cluster config is
// available.
func NewKubeAPIServerClientFromConfig(restConfig *rest.Config) (libencryptionstatus.KMSEncryptionStatusClient, error) {
	client, err := operatorclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build operator client: %w", err)
	}
	return NewKubeAPIServerClient(client.OperatorV1()), nil
}

type kubeAPIServerClient struct {
	getter operatorv1typed.KubeAPIServersGetter
}

func (c *kubeAPIServerClient) GetKMSEncryptionStatus(ctx context.Context) (*operatorv1.KMSEncryptionStatus, error) {
	obj, err := c.getter.KubeAPIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	s := obj.Status.EncryptionStatus
	return &s, nil
}

func (c *kubeAPIServerClient) ApplyKMSEncryptionStatus(ctx context.Context, fieldManager string, status *applyoperatorv1.KMSEncryptionStatusApplyConfiguration) error {
	// Encryption controllers use live clients (not listers) for strong consistency guarantees.
	// TODO: consider the extract-compare-skip pattern from dynamicOperatorClient.applyOperatorStatus
	// to avoid a redundant API server write on every sync.
	_, err := c.getter.KubeAPIServers().ApplyStatus(
		ctx,
		applyoperatorv1.KubeAPIServer("cluster").WithStatus(
			applyoperatorv1.KubeAPIServerStatus().WithEncryptionStatus(status),
		),
		metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	return err
}
