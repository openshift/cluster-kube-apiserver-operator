package rolloutmonitor

import (
	"context"
	"io/ioutil"
	"path"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/config/client"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
)

func NewInstallerCommand() *cobra.Command {
	// Only configure the rollout monitor for a single replica control plane.
	//
	// The transition between apiserver revisions for an HA control plane relies on
	// graceful termination and timeouts to work around load balancer behavior on
	// different cloud platforms. Rolling back in an HA control plane will
	// therefore require that the monitor be able to detect when the old revision
	// terminates so it can start health checking the new one.
	installerOptions := installerpod.NewInstallOptions().WithInitializeFn(
		func(ctx context.Context, o *installerpod.InstallOptions) error {
			clientConfig, err := client.GetKubeConfigOrInClusterConfig(o.KubeConfig, nil)
			if err != nil {
				return err
			}
			isSingleReplica, err := isSingleReplicaControlPlane(ctx, clientConfig)
			if err != nil {
				return err
			}
			if !isSingleReplica {
				return nil
			}

			// TODO Read the revision of the previous static pod (if
			// present) before the installer pod replaces it with a
			// new revision.

			// TODO Only configure the rollout monitor
			// if a previous revision exists. Without a previous
			// revision, there will be nothing to roll bac to.

			// TODO Configure copyRolloutMonitorManifest with the previous revision

			o.WithCopyContentFn(copyRolloutMonitorManifest)
			return nil
		})
	return installerpod.NewInstallerWithOptions(installerOptions)
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

// copyRolloutMonitorManifest copies the monitor manifest from the given resource
// dir to the manifest path.
func copyRolloutMonitorManifest(resourceDir, podManifestDir string) error {
	sourceFilename := path.Join(
		resourceDir,
		"configmaps",
		"rollout-monitor-pod",
		"pod.yaml",
	)
	podBytes, err := ioutil.ReadFile(sourceFilename)
	if err != nil {
		return err
	}

	// TODO update the manifest with the new revision and the revision
	// to roll back to if it fails.

	destFilename := path.Join(podManifestDir, "kube-apiserver-rollout-monitor-pod.yaml")
	klog.Infof("Writing kube-apiserver-rollout-monitor static pod manifest %q ...\n%s", destFilename, podBytes)
	if err := ioutil.WriteFile(destFilename, podBytes, 0644); err != nil {
		return err
	}

	return nil
}
