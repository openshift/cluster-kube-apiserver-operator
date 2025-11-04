package targetconfigcontroller

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/encryption/kms"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

// copied from https://github.com/flavianmissi/cluster-kube-apiserver-operator/tree/kms-plugin-sidecars

const (
	// KMSPluginImageEnvVar is the environment variable that specifies the KMS plugin container image
	// This should be set by the operator deployment
	KMSPluginImageEnvVar = "KMS_PLUGIN_IMAGE"

	// DefaultKMSPluginImage is the fallback image if KMS_PLUGIN_IMAGE is not set
	DefaultKMSPluginImage = "registry.k8s.io/kms-plugin/aws-encryption-provider:v1.0.0"
)

// getKMSEncryptionConfig checks if KMS encryption is enabled and returns the configuration
// Returns:
//   - kmsConfig: the KMS configuration if enabled, nil otherwise
//   - enabled: true if KMS encryption is enabled
//   - error: any error encountered while reading the config
func getKMSEncryptionConfig(ctx context.Context, apiserverLister configv1listers.APIServerLister) (*configv1.KMSConfig, bool, error) {
	apiserver, err := apiserverLister.Get("cluster")
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Info("APIServer config.openshift.io/cluster not found, KMS encryption not enabled")
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get APIServer config: %w", err)
	}

	// Check if encryption is configured
	if apiserver.Spec.Encryption.Type != configv1.EncryptionTypeKMS {
		klog.V(4).Infof("Encryption type is %q, not KMS - skipping KMS plugin injection", apiserver.Spec.Encryption.Type)
		return nil, false, nil
	}

	// KMS type is set, must have KMS config
	if apiserver.Spec.Encryption.KMS == nil {
		return nil, false, fmt.Errorf("encryption type is KMS but kms config is nil")
	}

	klog.Infof("KMS encryption enabled with type=%s, region=%s, keyARN=%s",
		apiserver.Spec.Encryption.KMS.Type,
		apiserver.Spec.Encryption.KMS.AWS.Region,
		apiserver.Spec.Encryption.KMS.AWS.KeyARN)

	return apiserver.Spec.Encryption.KMS, true, nil
}

// injectKMSPlugin adds the KMS plugin sidecar container to the kube-apiserver pod
// if KMS encryption is enabled in the cluster APIServer config
func injectKMSPlugin(ctx context.Context, pod *corev1.Pod, apiserverLister configv1listers.APIServerLister, kmsPluginImage string) error {
	// Check if KMS encryption is enabled
	kmsConfig, enabled, err := getKMSEncryptionConfig(ctx, apiserverLister)
	if err != nil {
		return fmt.Errorf("failed to check KMS encryption config: %w", err)
	}

	if !enabled {
		klog.V(4).Info("KMS encryption not enabled, skipping sidecar injection")
		return nil
	}

	// Validate the image is set
	if kmsPluginImage == "" {
		return fmt.Errorf("KMS plugin image is required when KMS encryption is enabled")
	}

	klog.Infof("Injecting KMS plugin sidecar container (image: %s)", kmsPluginImage)

	// Create container config for kube-apiserver
	// kube-apiserver uses hostNetwork: true, so it accesses AWS credentials via IMDS
	containerConfig := &kms.ContainerConfig{
		Image:          kmsPluginImage,
		UseHostNetwork: true, // Static pod with hostNetwork uses EC2 IMDS for AWS credentials
		KMSConfig:      kmsConfig,
	}

	// Inject the KMS plugin sidecar container and volumes into the pod spec
	if err := kms.AddKMSPluginToPodSpec(&pod.Spec, kmsConfig, containerConfig, true); err != nil {
		return fmt.Errorf("failed to inject KMS plugin sidecar: %w", err)
	}

	klog.Infof("Successfully injected KMS plugin sidecar container")
	return nil
}
