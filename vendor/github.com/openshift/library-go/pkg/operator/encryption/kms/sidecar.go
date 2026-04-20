package kms

import (
	"fmt"

	"github.com/openshift/library-go/pkg/operator/encryption/kms/plugins"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	corev1 "k8s.io/api/core/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

type KMSSidecarProvider interface {
	BuildSidecarContainer(containerName string, kmsConfig *apiserverv1.KMSConfiguration) (corev1.Container, error)
}

func NewSidecarProvider(config *state.KMSProviderConfig, credentials *corev1.Secret) (KMSSidecarProvider, error) {
	switch {
	case config.Vault != nil:
		return &plugins.VaultSidecarProvider{
			Config: config.Vault,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported KMS provider configuration")
	}
}

func AddSidecarContainer(podSpec *corev1.PodSpec, provider KMSSidecarProvider, containerName string, kmsConfig *apiserverv1.KMSConfiguration,
) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	container, err := provider.BuildSidecarContainer(containerName, kmsConfig)
	if err != nil {
		return fmt.Errorf("failed to build sidecar container: %w", err)
	}

	podSpec.Containers = append(podSpec.Containers, container)
	return nil
}
