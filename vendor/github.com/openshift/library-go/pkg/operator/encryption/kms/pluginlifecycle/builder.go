package pluginlifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/openshift/library-go/pkg/operator/encryption/encryptiondata"
)

// KMSPluginBuilder constructs KMS plugin pod spec contributions for injection
// into API server pods.
type KMSPluginBuilder struct {
	encryptionConfig           *encryptiondata.Config
	encryptionConfigSecretName string
	staticPod                  bool

	enableHealthReporter        bool
	healthReporterContainerName string
	healthReporterOperatorCmd   string
	healthReporterImage         string
}

// NewKMSPluginBuilder creates a builder that defaults to deployment mode.
func NewKMSPluginBuilder() *KMSPluginBuilder {
	return &KMSPluginBuilder{}
}

// FromEncryptionConfig loads all KMS plugins from a parsed encryption config.
// The encryptionConfigSecretName identifies the Secret the config was parsed
// from; it is used for volume configuration in both deployment and static pod
// modes.
func (b *KMSPluginBuilder) FromEncryptionConfig(encryptionConfigSecretName string, cfg *encryptiondata.Config) *KMSPluginBuilder {
	b.encryptionConfigSecretName = encryptionConfigSecretName
	b.encryptionConfig = cfg
	return b
}

// AsStaticPod switches the builder to static pod mode. Sidecars will reference
// data from the resource-dir volume and run as root (UID 0).
func (b *KMSPluginBuilder) AsStaticPod() *KMSPluginBuilder {
	b.staticPod = true
	return b
}

// WithHealthReporter enables injection of a health-reporter sidecar.
// containerName is used as both the container name and the subcommand name.
// operatorCmd is the parent binary (e.g. "cluster-kube-apiserver-operator").
func (b *KMSPluginBuilder) WithHealthReporter(containerName, operatorCmd, operatorImage string) *KMSPluginBuilder {
	b.enableHealthReporter = true
	b.healthReporterContainerName = containerName
	b.healthReporterOperatorCmd = operatorCmd
	b.healthReporterImage = operatorImage
	return b
}

// Apply mutates the given pod spec by injecting KMS plugin sidecars, volumes,
// and volume mounts. containerName identifies the API server container that
// needs the socket volume mount.
//
// It is a no-op (returns nil error) when no KMS plugins are found.
// It is idempotent.
func (b *KMSPluginBuilder) Apply(podSpec *corev1.PodSpec, containerName string) error {
	if podSpec == nil {
		return fmt.Errorf("pod spec cannot be nil")
	}
	if containerName == "" {
		return fmt.Errorf("container name cannot be empty")
	}

	kmsConfigurations, err := encryptiondata.ExtractUniqueAndSortedKMSConfigurations(b.encryptionConfig)
	if err != nil {
		return fmt.Errorf("failed to get KMS configurations: %w", err)
	}
	if len(kmsConfigurations) == 0 {
		klog.V(4).Infof("skipping KMS sidecar injection: no KMS plugins found in EncryptionConfiguration")
		return nil
	}

	var refDataVolumeName, refDataMountPath, referenceDataDir string
	if b.staticPod {
		refDataVolumeName = resourceDirVolumeName
		refDataMountPath = resourcesDir
		referenceDataDir = filepath.Join(resourcesDir, "secrets", b.encryptionConfigSecretName)
	} else {
		refDataVolumeName = referenceDataVolumeName
		refDataMountPath = referenceDataMountPath
		referenceDataDir = referenceDataMountPath
	}

	klog.V(4).Infof("injecting %d KMS sidecar(s)", len(kmsConfigurations))

	socketVolumeMount := corev1.VolumeMount{Name: kmsPluginSocketVolumeName, MountPath: kmsPluginSocketMountPath, ReadOnly: false}
	refDataVolumeMount := corev1.VolumeMount{Name: refDataVolumeName, MountPath: refDataMountPath, ReadOnly: true}

	for _, kmsConfiguration := range kmsConfigurations {
		// ExtractUniqueAndSortedKMSConfigurations function rewrites the .Name field to include only the key ID
		keyID := kmsConfiguration.Name

		pluginConfig, ok := b.encryptionConfig.KMSPlugins[keyID]
		if !ok {
			return fmt.Errorf("missing plugin config for keyID %s", keyID)
		}

		refData := &referenceDataResolver{
			pluginsSecretData:    b.encryptionConfig.KMSPluginsSecretData,
			pluginsConfigMapData: b.encryptionConfig.KMSPluginsConfigMapData,
			referenceDataDir:     referenceDataDir,
			keyID:                keyID,
		}

		provider, err := newSidecarProvider(keyID, kmsConfiguration.Endpoint, pluginConfig, refData)
		if err != nil {
			return fmt.Errorf("failed to create a sidecar provider for keyID %s: %w", keyID, err)
		}

		if err := ensureSidecarContainer(podSpec, provider); err != nil {
			return err
		}

		if err := ensureVolumeMountInContainer(podSpec.InitContainers, provider.Name(), socketVolumeMount); err != nil {
			return err
		}

		if err := ensureVolumeMountInContainer(podSpec.InitContainers, provider.Name(), refDataVolumeMount); err != nil {
			return err
		}

		if b.staticPod {
			if err := setRunAsRoot(podSpec.InitContainers, provider.Name()); err != nil {
				return err
			}
		}
	}

	if err := ensureVolumeMountInContainer(podSpec.Containers, containerName, socketVolumeMount); err != nil {
		return err
	}

	if err := ensureSocketVolume(podSpec); err != nil {
		return err
	}

	if !b.staticPod {
		if err := ensureReferenceDataVolume(podSpec, b.encryptionConfigSecretName); err != nil {
			return err
		}
	}

	if b.enableHealthReporter {
		if b.healthReporterImage == "" {
			return fmt.Errorf("health reporter image is required when WithHealthReporter is used")
		}

		sockets := make([]string, 0, len(kmsConfigurations))
		for _, kmsConfiguration := range kmsConfigurations {
			sockets = append(sockets, kmsConfiguration.Endpoint)
		}

		var kubeconfig string
		if b.staticPod {
			kubeconfig = defaultStaticPodKubeconfig
		}

		reporter := &healthReporter{name: b.healthReporterContainerName, operatorCmd: b.healthReporterOperatorCmd, image: b.healthReporterImage, sockets: sockets, kubeconfig: kubeconfig}
		if err := ensureSidecarContainer(podSpec, reporter); err != nil {
			return err
		}

		healthReporterSocketMount := corev1.VolumeMount{Name: kmsPluginSocketVolumeName, MountPath: kmsPluginSocketMountPath, ReadOnly: true}
		if err := ensureVolumeMountInContainer(podSpec.InitContainers, reporter.Name(), healthReporterSocketMount); err != nil {
			return err
		}

		if b.staticPod {
			resourceDirMount := corev1.VolumeMount{Name: resourceDirVolumeName, MountPath: resourcesDir, ReadOnly: true}
			if err := ensureVolumeMountInContainer(podSpec.InitContainers, reporter.Name(), resourceDirMount); err != nil {
				return err
			}
			if err := setRunAsRoot(podSpec.InitContainers, reporter.Name()); err != nil {
				return err
			}
		}
	}

	return nil
}

// defaultStaticPodKubeconfig is reused from the cert-syncer kubeconfig because
// in-cluster config does not work on host-network static pods (kubernetes.default.svc
// does not resolve). This matches the pattern used by the startup monitor.
// TODO: move to a dedicated least-privilege kubeconfig once available.
const defaultStaticPodKubeconfig = "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-cert-syncer-kubeconfig/kubeconfig"

type healthReporter struct {
	name        string
	operatorCmd string
	image       string
	sockets     []string
	kubeconfig  string
}

func (h *healthReporter) Name() string {
	return h.name
}

func (h *healthReporter) BuildSidecarContainer() (corev1.Container, error) {
	args := []string{
		fmt.Sprintf("--kms-sockets=%s", strings.Join(h.sockets, ",")),
		"--node-name=$(NODE_NAME)",
	}
	if h.kubeconfig != "" {
		args = append(args, fmt.Sprintf("--kubeconfig=%s", h.kubeconfig))
	}

	return corev1.Container{
		Name:                     h.name,
		Image:                    h.image,
		Command:                  []string{h.operatorCmd, h.name},
		Args:                     args,
		ImagePullPolicy:          corev1.PullIfNotPresent,
		RestartPolicy:            ptr.To(corev1.ContainerRestartPolicyAlways),
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		Env: []corev1.EnvVar{
			{
				Name: "NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("32Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   ptr.To(true),
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}, nil
}

func fetchEncryptionConfig(ctx context.Context, encryptionConfigNamespace, encryptionConfigSecretName string, secretClient corev1client.SecretsGetter) (*encryptiondata.Config, error) {
	encryptionConfigurationSecret, err := secretClient.Secrets(encryptionConfigNamespace).Get(ctx, encryptionConfigSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(4).Infof("skipping KMS sidecar injection: %s/%s secret not found", encryptionConfigNamespace, encryptionConfigSecretName)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s secret: %w", encryptionConfigNamespace, encryptionConfigSecretName, err)
	}

	encryptionConfig, err := encryptiondata.FromSecret(encryptionConfigurationSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to extract encryption config from %s/%s secret: %w", encryptionConfigNamespace, encryptionConfigSecretName, err)
	}

	if encryptionConfig == nil {
		return nil, fmt.Errorf("encryption configuration is required in %s/%s secret", encryptionConfigNamespace, encryptionConfigSecretName)
	}

	return encryptionConfig, nil
}
