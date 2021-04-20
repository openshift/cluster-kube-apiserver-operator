package gracefulmonitor

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
)

func NewInstallerCommand() *cobra.Command {
	installerOptions := installerpod.NewInstallOptions().WithInitializeFn(
		func(ctx context.Context, o *installerpod.InstallOptions) error {
			isSingleReplica, err := isSingleReplicaControlPlane(ctx, o.ProtoConfig)
			if err != nil {
				return err
			}
			if isSingleReplica {
				// Only configure graceful rollout for a single replica control plane
				return configureGracefulRollout(o)
			}
			return nil
		})
	return installerpod.NewInstallerWithOptions(installerOptions)
}

// configureGracefulRollout configures the installer for graceful rollout by:
//
// - ensuring only the oldest static pod manifest will be present in the manifest path
// - ensuring that the new static pod will have unique names and ports
// - ensuring a static pod will be created for the graceful monitor
//
// These changes allow for a new apiserver to be started and for the active one only
// be replaced once the new apiserver passes a readyz check.
func configureGracefulRollout(o *installerpod.InstallOptions) error {
	klog.V(1).Info("Configuring graceful rollout for a single replica control plane")

	manifests, err := ReadAPIServerManifests(o.PodManifestDir)
	if err != nil {
		return err
	}
	activeManifest := manifests.ActiveManifest()

	// Ensure only the active pod manifest exists in the manifest path before
	// creating a new pod manifest.
	if err := pruneInactiveManifests(manifests, activeManifest); err != nil {
		return err
	}

	// Enable inclusion of the pod revision in the pod name and manifest
	// filename to ensure uniqueness. This allows concurrent execution of
	// multiple pods during a graceful transition.
	o.WithManifestFilenameFn(setRevisionInManifestFilename)
	o.WithPodMutationFn(setRevisionInPodName)

	// Enable customization of the pod yaml by substituting the log filenames
	// and ports in the revision's configmaps. This works because the pod yaml
	// is delivered to the node as a configmap.
	// TODO(marun) Limit substitution to the pod
	o.WithSubstitutePodFn(func(input string) string {
		// Determine the port map to use for substituting configmap content.
		activePort := 0
		if activeManifest != nil {
			activePort = activeManifest.Port
		}
		portMap := NextPortMap(activePort)

		// Prevent concurrent writes to the same log file by including the pod's
		// secure port in log filenames. Only a single pod for a given secure port
		// can run at a time due to the init container check.
		//
		// TODO(marun) Ensure uniquely-named logs are rotated/culled
		result := substituteLogFilenames(input, portMap[6443])

		// Replace the default ports with the ports that will be forwarded to
		return substitutePorts(result, portMap)
	})

	// Enable copying the graceful monitor manifest to manage port forwarding to
	// the active static pod
	o.WithCopyContentFn(copyGracefulMonitorManifest)

	return nil
}

// isSingleReplicaControlPlane indicates whether the cluster is using a single node
// control plane topology.
func isSingleReplicaControlPlane(ctx context.Context, c *rest.Config) (bool, error) {
	configClient, err := configv1client.NewForConfig(c)
	if err != nil {
		return false, err
	}
	infra, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode, nil
}

// copyGracefulMonitorManifest copies the monitor manifest from the given resource
// dir to the manifest path.
func copyGracefulMonitorManifest(resourceDir, podManifestDir string) error {
	sourceFilename := path.Join(
		resourceDir,
		"configmaps",
		"graceful-monitor-pod",
		"pod.yaml",
	)
	podBytes, err := ioutil.ReadFile(sourceFilename)
	if err != nil {
		return err
	}

	destFilename := path.Join(podManifestDir, "graceful-monitor-pod.yaml")
	klog.Infof("Writing graceful-monitor static pod manifest %q ...\n%s", destFilename, podBytes)
	if err := ioutil.WriteFile(destFilename, podBytes, 0644); err != nil {
		return err
	}

	return nil
}

// setRevisionInPodName ensures the pod name includes its revision so that the
// static pods for a node are differentiated in the API.
func setRevisionInPodName(pod *corev1.Pod) error {
	revision := pod.Labels["revision"]
	revSuffix := fmt.Sprintf("-%s", revision)
	pod.Name = pod.Name + revSuffix
	return nil
}

// setRevisionInManifestFilename ensures the manifest filename is unique by
// including the pod revision so that multiple apiserver manifests can exist in the
// manifests path.
func setRevisionInManifestFilename(baseFilename string, pod *corev1.Pod) string {
	// Ensure
	revision := pod.Labels["revision"]
	return fmt.Sprintf("%s-%s.yaml", baseFilename, revision)
}

// pruneInactiveManifests deletes all but the active manifest to ensure that the
// new revision of the apiserver static pod can bind to its configured ports.
func pruneInactiveManifests(manifests StaticPodManifests, activeManifest *StaticPodManifest) error {
	for _, manifest := range manifests {
		if activeManifest != nil && activeManifest.Filename == manifest.Filename {
			continue
		}
		if err := os.Remove(manifest.Filename); err != nil {
			return err
		}

		// Not necessary to wait for terminatation since the pod
		// init container waits for the ports to be freed.
	}
	return nil
}

// substituteLogFilenames suffixes known log filenames in the input with the
// provided port.
func substituteLogFilenames(input string, port int) string {
	logSuffix := fmt.Sprintf("-%d", port)
	result := strings.ReplaceAll(
		input,
		"/var/log/kube-apiserver/audit.log",
		fmt.Sprintf("/var/log/kube-apiserver/audit%s.log", logSuffix),
	)
	result = strings.ReplaceAll(
		result,
		"/var/log/kube-apiserver/.terminating",
		fmt.Sprintf("/var/log/kube-apiserver/.terminating%s", logSuffix),
	)
	result = strings.ReplaceAll(
		result,
		"/var/log/kube-apiserver/termination.log",
		fmt.Sprintf("/var/log/kube-apiserver/termination%s.log", logSuffix),
	)
	return result
}

// substitutePorts replaces the default ports in the input with the ports that will
// be forwarded to
func substitutePorts(input string, portMap map[int]int) string {
	result := input
	for port, substitutePort := range portMap {
		result = strings.ReplaceAll(result, fmt.Sprintf("%d", port), fmt.Sprintf("%d", substitutePort))
	}
	return result
}
