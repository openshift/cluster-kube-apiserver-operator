package operator

import (
	"github.com/spf13/cobra"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
)

func NewOperator() *cobra.Command {
	cmd := controllercmd.
		NewControllerCommandConfig("openshift-cluster-kube-apiserver-operator", version.Get(), operator.RunOperator).
		NewCommand()
	cmd.Use = "operator"
	cmd.Short = "Start the Cluster kube-apiserver Operator"

	return cmd
}
