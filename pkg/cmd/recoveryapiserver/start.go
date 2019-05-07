package recoveryapiserver

import (
	"context"
	"fmt"
	"path"

	"github.com/spf13/cobra"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/klog"

	"k8s.io/client-go/tools/watch"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

type CreateOptions struct {
	PodManifestDir string

	StaticPodResourcesDir string
}

func NewStartCommand() *cobra.Command {
	o := &CreateOptions{
		PodManifestDir:        "/etc/kubernetes/manifests",
		StaticPodResourcesDir: "/etc/kubernetes/static-pod-resources",
	}

	cmd := &cobra.Command{
		Use: "start",
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

	return cmd
}

func (o *CreateOptions) Complete() error {
	return nil
}

func (o *CreateOptions) Validate() error {
	return nil
}

func (o *CreateOptions) Run() error {
	ctx, cancel := watch.ContextWithOptionalTimeout(context.TODO(), 0 /*infinity*/)
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
	// destroy the server when we're done
	defer func() {
		if err := recoveryApiserver.Destroy(); err != nil {
			klog.Errorf("failed to destroy the recovery apiserver")
		}
	}()

	kubeconfigPath := path.Join(recoveryApiserver.GetRecoveryResourcesDir(), recovery.AdminKubeconfigFileName)
	klog.Infof("Waiting for recovery apiserver to be healthy.")
	klog.Infof("    export KUBECONFIG=%s", kubeconfigPath)
	klog.Infof("to access the server.")

	err = recoveryApiserver.WaitForHealthz(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for recovery apiserver to be ready: %v", err)
	}
	klog.Infof("Recovery apiserver is up.")

	<-ctx.Done()
	klog.Infof("Exit requested.")

	return nil
}
