package pluginlifecycle

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// defaultStaticPodKubeconfig is reused from the cert-syncer kubeconfig because
// in-cluster config does not work on host-network static pods (kubernetes.default.svc
// does not resolve). This matches the pattern used by the startup monitor.
// TODO: move to a dedicated least-privilege kubeconfig once available.
const defaultStaticPodKubeconfig = "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-cert-syncer-kubeconfig/kubeconfig"

func (b *KMSPluginBuilder) applyHealthReporter(podSpec *corev1.PodSpec, sockets []string) error {
	if b.healthReporterImage == "" {
		return fmt.Errorf("health reporter image is required when WithHealthReporter is used")
	}

	var kubeconfig string
	if b.staticPod {
		kubeconfig = defaultStaticPodKubeconfig
	}

	reporter := &healthReporter{name: b.healthReporterContainerName, operatorCmd: b.healthReporterOperatorCmd, image: b.healthReporterImage, sockets: sockets, kubeconfig: kubeconfig}
	if err := ensureSidecarContainer(podSpec, reporter); err != nil {
		return err
	}

	socketMount := corev1.VolumeMount{Name: kmsPluginSocketVolumeName, MountPath: kmsPluginSocketMountPath, ReadOnly: true}
	if err := ensureVolumeMountInContainer(podSpec.InitContainers, reporter.Name(), socketMount); err != nil {
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

	return nil
}

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
