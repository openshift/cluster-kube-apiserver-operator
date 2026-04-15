package targetconfigcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/apis/apiserver/install"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

var (
	cfgScheme = runtime.NewScheme()
	codecs    = serializer.NewCodecFactory(cfgScheme, serializer.EnableStrict)
)

func init() {
	install.Install(cfgScheme)
}

// AddKMSPluginToPodSpec conditionally adds the KMS plugin volume mount to the specified container.
// It assumes the pod spec does not already contain the KMS volume or mount; no deduplication is performed.
func AddKMSPluginToPodSpec(podSpec *corev1.PodSpec, featureGateAccessor featuregates.FeatureGateAccess, secretLister corev1listers.SecretLister, kmsPluginImage string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}

	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil
	}

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("failed to get feature gates: %w", err)
	}

	if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
		klog.Infof("kms is disabled: feature gate %s is disabled", features.FeatureGateKMSEncryption)
		return nil
	}

	encryptionConfig, err := secretLister.Secrets("openshift-config-managed").Get("encryption-config-openshift-kube-apiserver")
	if apierrors.IsNotFound(err) {
		klog.Infof("kms is disabled: secret openshift-config/encryption-config not found: %v", err)
		return nil
	}
	if err != nil {
		klog.Infof("kms is disabled: failed to get encryption-config-openshift-kube-apiserver secret: %v", err)
		return nil
	}

	encryptionConfigBytes, ok := encryptionConfig.Data["encryption-config"]
	if !ok {
		klog.Infof("kms is disabled: failed to get encryption-config key in secret")
		return nil
	}

	gvk := apiserverv1.SchemeGroupVersion.WithKind("EncryptionConfiguration")
	obj, _, err := codecs.UniversalDeserializer().Decode(encryptionConfigBytes, &gvk, nil)
	if err != nil {
		return fmt.Errorf("kms is disabled: failed to decode: %w", err)
	}

	config := obj.(*apiserverv1.EncryptionConfiguration)
	kmsEndpoints := extractUniqueKMSEndpoints(config)
	if len(kmsEndpoints) == 0 {
		klog.Infof("kms is disabled: no KMS providers found in encryption config")
		return nil
	}

	klog.Infof("kms is enabled: injecting %d KMS plugin sidecar(s) into kube-apiserver pod", len(kmsEndpoints))

	addKMSSocketVolume(podSpec)

	// Add socket volume mount to kube-apiserver.
	if err := addVolumeMountToContainer(podSpec, "kube-apiserver", "kms-plugin-socket", "/var/run/kmsplugin"); err != nil {
		return err
	}

	// Add one sidecar per unique KMS key.
	for keyID := range kmsEndpoints {
		containerName := fmt.Sprintf("kms-plugin-%s", keyID)
		listenAddress := fmt.Sprintf("unix:///var/run/kmsplugin/kms-%s.sock", keyID)
		addKMSPluginSidecarContainer(podSpec, containerName, kmsPluginImage, listenAddress)
	}

	return nil
}

// extractUniqueKMSEndpoints returns a map of keyID -> endpoint for all unique
// KMS providers. Multiple resources that share the same endpoint are deduplicated.
func extractUniqueKMSEndpoints(config *apiserverv1.EncryptionConfiguration) map[string]string {
	endpoints := make(map[string]string)
	seenEndpoints := make(map[string]bool)

	for _, rc := range config.Resources {
		for _, provider := range rc.Providers {
			if provider.KMS == nil {
				continue
			}
			endpoint := provider.KMS.Endpoint
			if seenEndpoints[endpoint] {
				continue
			}
			seenEndpoints[endpoint] = true

			// Provider name format: "{keyID}_{resource}", e.g. "1_secrets"
			keyID := extractKeyIDFromProviderName(provider.KMS.Name)
			endpoints[keyID] = endpoint
		}
	}
	return endpoints
}

// extractKeyIDFromProviderName extracts the key ID from a KMS provider name.
func extractKeyIDFromProviderName(name string) string {
	for i, c := range name {
		if c == '_' {
			return name[:i]
		}
	}
	return name
}

// addKMSSocketVolume adds the shared emptyDir volume for KMS plugin sockets.
func addKMSSocketVolume(podSpec *corev1.PodSpec) {
	for _, v := range podSpec.Volumes {
		if v.Name == "kms-plugin-socket" {
			return
		}
	}
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: "kms-plugin-socket",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
}

// addVolumeMountToContainer adds a volume mount to the named container.
func addVolumeMountToContainer(podSpec *corev1.PodSpec, containerName, volumeName, mountPath string) error {
	for i, container := range podSpec.Containers {
		if container.Name == containerName {
			podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeName,
					MountPath: mountPath,
				},
			)
			return nil
		}
	}
	return fmt.Errorf("container %s not found", containerName)
}

// addKMSPluginSidecarContainer adds a sidecar container for a single KMS plugin.
func addKMSPluginSidecarContainer(podSpec *corev1.PodSpec, containerName, image, listenAddress string) {
	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            containerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/mock-vault-kms"},
		Args:            []string{fmt.Sprintf("--listen-address=%s", listenAddress)},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "resource-dir",
				MountPath: "/etc/kubernetes/static-pod-resources",
			},
			{
				Name:      "kms-plugin-socket",
				MountPath: "/var/run/kmsplugin",
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptr.To(true),
		},
	})
}

func KMSRevisionPrecondition(kubeClient kubernetes.Interface, featureGateAccessor featuregates.FeatureGateAccess) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		// Skip the check entirely when the KMS feature gate is disabled.
		if !featureGateAccessor.AreInitialFeatureGatesObserved() {
			return true, nil
		}
		featureGates, err := featureGateAccessor.CurrentFeatureGates()
		if err != nil {
			return true, nil
		}
		if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
			return true, nil
		}

		encryptionConfig, err := kubeClient.CoreV1().Secrets("openshift-config-managed").Get(ctx, "encryption-config-openshift-kube-apiserver", metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("kms precondition: failed to get encryption-config secret: %w", err)
		}

		kmsConfigured := hasKMSEncryptionProvider(encryptionConfig)

		podCM, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "kube-apiserver-pod", metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			// Pod configmap not created yet — let the revision controller proceed.
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("kms precondition: failed to get kube-apiserver-pod configmap: %w", err)
		}

		kmsInPod := podHasKMSSidecar(podCM)

		if kmsConfigured != kmsInPod {
			klog.Infof("kms revision precondition not met: kmsConfigured=%v kmsInPod=%v, waiting for reconciliation", kmsConfigured, kmsInPod)
			return false, nil
		}
		return true, nil
	}
}

func podHasKMSSidecar(podCM *corev1.ConfigMap) bool {
	podYAML, ok := podCM.Data["pod.yaml"]
	if !ok {
		return false
	}

	var pod corev1.Pod
	if err := json.Unmarshal([]byte(podYAML), &pod); err != nil {
		return false
	}

	return slices.ContainsFunc(pod.Spec.Containers, func(c corev1.Container) bool {
		return strings.HasPrefix(c.Name, "kms-plugin-")
	})
}

func hasKMSEncryptionProvider(encryptionConfig *corev1.Secret) bool {
	encryptionConfigBytes, ok := encryptionConfig.Data["encryption-config"]
	if !ok {
		return false
	}

	gvk := apiserverv1.SchemeGroupVersion.WithKind("EncryptionConfiguration")
	obj, _, err := codecs.UniversalDeserializer().Decode(encryptionConfigBytes, &gvk, nil)
	if err != nil {
		return false
	}

	config := obj.(*apiserverv1.EncryptionConfiguration)
	return slices.ContainsFunc(config.Resources, func(resource apiserverv1.ResourceConfiguration) bool {
		return slices.ContainsFunc(resource.Providers, func(provider apiserverv1.ProviderConfiguration) bool {
			return provider.KMS != nil
		})
	})
}
