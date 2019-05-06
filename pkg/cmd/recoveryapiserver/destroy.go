package recoveryapiserver

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

type DestroyOptions struct {
	Options
}

func NewDestroyCommand() *cobra.Command {
	o := &DestroyOptions{
		Options: NewDefaultOptions(),
	}

	cmd := &cobra.Command{
		Use: "destroy",
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
	}

	return cmd
}

func (o *DestroyOptions) Complete() error {
	err := o.Options.Complete()
	if err != nil {
		return err
	}

	return nil
}

func (o *DestroyOptions) Validate() error {
	err := o.Options.Validate()
	if err != nil {
		return err
	}

	return nil
}

func (o *DestroyOptions) Run() error {
	apiserver := &recovery.Apiserver{
		PodManifestDir: o.PodManifestDir,
	}

	err := apiserver.Destroy()
	if err != nil {
		return fmt.Errorf("failed to destroy recovery apiserver: %v", err)
	}

	return nil
}
