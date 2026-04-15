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

	// Add shared volumes and init container once (not per-key).
	addKMSSocketVolume(podSpec)
	addSoftHSMTokensVolume(podSpec)
	addSoftHSMInitContainer(podSpec, kmsPluginImage)

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

// Pre-generated SoftHSM token with AES-256 key (base64-encoded tar.gz).
// This is the same data from the k8s-mock-kms-plugin ConfigMap.
// Run k8s-mock-plugin-key-gen/generate.sh to regenerate.
const softhsmTokensB64 = `H4sIAAAAAAAAA9PTZ6A5MDAwMDQ3NwfRIIBOg9mGpoYmQFVmpsZmQHFzcyMzBgVT2juNgaG0uCSx
SEGBoSg/vwSfOkLyQxTo6SclWSalGaYk6SanJVvqmhmaGemaGxumAlkG5gbmJmZmhpaplCUSUASb
Y8Y77vg3NDExH41/ugAi4z89NS+1KLEkMz+PDDtAEWyGL/4N0ePfzNTElEHBgOq+xQJGePxDAdNA
O2AUDAwgMv8D62+jNEsjS91EQ8tkXVOTlFRdy9REE13TxCQjU2PjRBMjixS9nPzkbGx2EMz/6OW/
kZGhseFo/qcHoGb85ydlpSZjCSQC8W9oZGqGHv8mZqPlPz2BMhofVh+wQGlGGP0fTQFMgoEZjebI
zi3WLUkthgWaIJq8wtmYw16NO3743+n8IbrBQ654o8P1RS3cTE2rvqxes3f2681QdW3oFk1At9A9
/CyaEpjj5KHiTGhaYEbC+HDfwb0L9SYjK7oAG7oAO7oAB7oAF7oAD7q1Ajjchx5mMPFEKAPmLwWo
eBK6wcnoVqegC6Sia0lDNVugASpegK6zEF2gCE2ACeYtiNkODEww/8CCGSYuhF0cFtYMrAyjgEaA
yPK/JD87NQ9nBU8AkFz/G5qZmBmMlv/0ASTFP64KngAgFP+mGPFvbmBgPBr/dATgOqqBIdgTyofX
0rBaXAEHgOrzQtMnAEpCyCkIqs4bKg+tXVh0oeI+aPo9hLuLzFeazTs0z0uff0K8jyJbufak7QIT
v1jacNd/lLvW0Bink9F64W7jVjf1Y/o3JrhXvTgRdIo7qjH6Q94aa7Wis/x+aYIib+56Qc33RTd/
mrLoLKtVa1bK/Mt6/MqI48T9mDkzbzYfleKNm8ytfO3yvnVnmdZzNTxQ5330z2/9zpt+B+dP+bn+
UWq1aNfvZvMIhu2djYvqNbukYbXlKBgFo2AUDDkAAM1LQHIAGgAA`

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

// addSoftHSMTokensVolume adds the hostPath volume for SoftHSM tokens (persistence across restarts).
func addSoftHSMTokensVolume(podSpec *corev1.PodSpec) {
	for _, v := range podSpec.Volumes {
		if v.Name == "softhsm-tokens" {
			return
		}
	}
	softhsmTokensHostPathType := corev1.HostPathDirectoryOrCreate
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: "softhsm-tokens",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/var/lib/softhsm/tokens",
				Type: &softhsmTokensHostPathType,
			},
		},
	})
}

// addSoftHSMInitContainer adds a single init container that bootstraps SoftHSM tokens
// from embedded data to a hostPath. If tokens already exist on disk, initialization is skipped.
func addSoftHSMInitContainer(podSpec *corev1.PodSpec, image string) {
	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
		Name:            "init-softhsm",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/bin/sh", "-c"},
		Args: []string{fmt.Sprintf(`
set -e
set -x
if [ $(ls -1 /var/lib/softhsm/tokens 2>/dev/null | wc -l) -ge 1 ]; then
  echo "Skipping initialization of softhsm"
  exit 0
fi
mkdir -p /var/lib/softhsm/tokens
cd /var/lib/softhsm/tokens
cat <<'TOKENEOF' | base64 -d | tar xzf -
%s
TOKENEOF`, softhsmTokensB64),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "softhsm-tokens",
				MountPath: "/var/lib/softhsm/tokens",
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: ptr.To(true),
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
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/bin/sh", "-c"},
		Args: []string{fmt.Sprintf(`
cat > /tmp/softhsm-config.json <<'EOF'
{"Path":"/usr/lib/softhsm/libsofthsm2.so","TokenLabel":"kms-test","Pin":"1234"}
EOF
rm -f %[1]s
exec /usr/local/bin/mock-kms-plugin -listen-addr=%[1]s -config-file-path=/tmp/softhsm-config.json`,
			listenAddress),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "resource-dir",
				MountPath: "/etc/kubernetes/static-pod-resources",
			},
			{
				Name:      "kms-plugin-socket",
				MountPath: "/var/run/kmsplugin",
			},
			{
				Name:      "softhsm-tokens",
				MountPath: "/var/lib/softhsm/tokens",
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
