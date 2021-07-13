package startupmonitor

import (
	"context"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/openshift/library-go/pkg/config/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type Options struct {
	// Revision identifier for this particular installation instance
	Revision int

	// FallbackTimeout specifies a timeout after which the monitor starts the fall back procedure
	FallbackTimeout time.Duration

	// ResourceDir directory that holds all files supporting the static pod manifest
	ResourceDir string

	// ManifestDir directory for the static pod manifest
	ManifestDir string

	// TargetName identifies operand used to construct the final file name when reading the current and previous manifests
	TargetName string

	// KubeConfig file for authn/authz against Kube API
	KubeConfig string

	// HealthChecker defines a type that abstracts away assessing operand's health condition.
	// This is an extention point for the operators to provide a custom health function for their operands
	HealthChecker HealthChecker

	// resConfig derived from the kubeconfig that will be used by some health checkers
	restConfig *rest.Config
}

func NewCommand(healthChecker HealthChecker) *cobra.Command {
	o := Options{HealthChecker: healthChecker}

	cmd := &cobra.Command{
		Use:   "startup-monitor",
		Short: "Monitors the provided static pod revision and if it proves unhealthy rolls back to the previous revision.",
		Run: func(cmd *cobra.Command, args []string) {
			klog.V(1).Info(cmd.Flags())
			klog.V(1).Info(spew.Sdump(o))

			if err := o.Validate(); err != nil {
				klog.Exit(err)
			}
			if err := o.Complete(); err != nil {
				klog.Exit(err)
			}
			if err := o.Run(); err != nil {
				klog.Exit(err)
			}
		},
	}

	o.AddFlags(cmd.Flags())
	return cmd
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.KubeConfig, "kubeconfig", o.KubeConfig, "kubeconfig file or empty")
	fs.IntVar(&o.Revision, "revision", o.Revision, "identifier for this particular installation instance")
	fs.DurationVar(&o.FallbackTimeout, "fallback-timeout-duration", 33*time.Second, "maximum time in seconds to wait for the operand to become healthy (default 33s)")
	fs.StringVar(&o.ResourceDir, "resource-dir", o.ResourceDir, "directory that holds all files supporting the static pod manifests")
	fs.StringVar(&o.ManifestDir, "manifests-dir", o.ManifestDir, "directory for the static pod manifest")
	fs.StringVar(&o.TargetName, "target-name", o.TargetName, "identifies operand used to construct the final file name when reading the current and previous manifests")
}

func (o *Options) Validate() error {
	if o.FallbackTimeout == 0 {
		return fmt.Errorf("--fallback-timeout-duration cannot be 0")
	}
	if len(o.ResourceDir) == 0 {
		return fmt.Errorf("--resource-dir is required")
	}
	if len(o.ManifestDir) == 0 {
		return fmt.Errorf("--manifests-dir is required")
	}
	if len(o.TargetName) == 0 {
		return fmt.Errorf("--target-name is required")
	}
	return nil
}

func (o *Options) Complete() error {
	if len(o.KubeConfig) == 0 {
		return nil
	}

	clientConfig, err := client.GetKubeConfigOrInClusterConfig(o.KubeConfig, nil)
	if err != nil {
		return err
	}

	o.restConfig = rest.CopyConfig(clientConfig)
	return nil
}

func (o *Options) Run() error {
	shutdownCtx := setupSignalContext(context.TODO())

	m := newMonitor(o.restConfig, o.HealthChecker).
		withRevision(o.Revision).
		withManifestPath(o.ManifestDir).
		withStaticPodResourcesPath(o.ResourceDir).
		withTargetName(o.TargetName).
		withProbeTimeout(o.FallbackTimeout).
		withProbeInterval(time.Second)

	return m.Run(shutdownCtx)
}
