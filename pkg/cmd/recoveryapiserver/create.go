package recoveryapiserver

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/klog"

	"k8s.io/client-go/tools/watch"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

type CreateOptions struct {
	Options

	StaticPodResourcesDir string
	Timeout               time.Duration
	Wait                  bool
}

func NewCreateCommand() *cobra.Command {
	o := &CreateOptions{
		Options:               NewDefaultOptions(),
		StaticPodResourcesDir: "/etc/kubernetes/static-pod-resources",
		Timeout:               5 * time.Minute,
		Wait:                  true,
	}

	cmd := &cobra.Command{
		Use: "create",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := o.Complete()
			if err != nil {
				return err
			}

			err = o.Validate()
			if err != nil {
				return err
			}

			err = o.Run()
			if err != nil {
				return err
			}

			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "startup timeout, 0 means infinite")
	cmd.Flags().BoolVar(&o.Wait, "wait", o.Wait, "wait for recovery apiserver to become ready")

	return cmd
}

func (o *CreateOptions) Complete() error {
	err := o.Options.Complete()
	if err != nil {
		return err
	}

	return nil
}

func (o *CreateOptions) Validate() error {
	err := o.Options.Validate()
	if err != nil {
		return err
	}

	return nil
}

func (o *CreateOptions) Run() error {
	ctx, cancel := watch.ContextWithOptionalTimeout(context.TODO(), o.Timeout)
	defer cancel()

	signalHandler := server.SetupSignalHandler()
	go func() {
		<-signalHandler
		cancel()
	}()

	recoveryApiserver := &recovery.Apiserver{
		PodManifestDir:        o.PodManifestDir,
		StaticPodResourcesDir: o.StaticPodResourcesDir,
	}

	err := recoveryApiserver.Create()
	if err != nil {
		return fmt.Errorf("failed to create recovery apiserver: %v", err)
	}

	kubeconfigPath := path.Join(recoveryApiserver.GetRecoveryResourcesDir(), recovery.AdminKubeconfigFileName)
	klog.Infof("system:admin kubeconfig written to %q", kubeconfigPath)

	if !o.Wait {
		return nil
	}

	klog.Infof("Waiting for recovery apiserver to come up.")
	err = recoveryApiserver.WaitForHealthz(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for recovery apiserver to be ready: %v", err)
	}
	klog.Infof("Recovery apiserver is up.")

	return nil
}
