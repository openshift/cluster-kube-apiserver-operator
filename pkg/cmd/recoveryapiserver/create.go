package recoveryapiserver

import (
	"fmt"
	"path"

	"github.com/spf13/cobra"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

type CreateOptions struct {
	Options

	StaticPodResourcesDir string
}

func NewCreateCommand() *cobra.Command {
	o := &CreateOptions{
		Options:               NewDefaultOptions(),
		StaticPodResourcesDir: "/etc/kubernetes/static-pod-resources",
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
	recoveryApiserver := &recovery.Apiserver{
		PodManifestDir:        o.PodManifestDir,
		StaticPodResourcesDir: o.StaticPodResourcesDir,
	}

	err := recoveryApiserver.Create()
	if err != nil {
		return fmt.Errorf("failed to create recovery apiserver: %v", err)
	}

	kubeconfigPath := path.Join(recoveryApiserver.GetRecoveryResourcesDir(), recovery.AdminKubeconfigFileName)
	klog.Infof("To access the server.")
	klog.Infof("    export KUBECONFIG=%s", kubeconfigPath)

	return nil
}
