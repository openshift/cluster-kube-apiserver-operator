package main

import (
	"context"
	goflag "flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/client-go/rest"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/certregenerationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/checkendpoints"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/insecurereadyz"
	operatorcmd "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/render"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/resourcegraph"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/startupmonitorreadiness"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/operator/staticpod/certsyncpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/prune"
	"github.com/openshift/library-go/pkg/operator/staticpod/startupmonitor"

	operatorclientv1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	command := NewOperatorCommand(context.Background())
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func NewOperatorCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-kube-apiserver-operator",
		Short: "OpenShift cluster kube-apiserver operator",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(operatorcmd.NewOperator())
	cmd.AddCommand(render.NewRenderCommand())
	cmd.AddCommand(installerpod.NewInstaller())
	cmd.AddCommand(prune.NewPrune())
	cmd.AddCommand(resourcegraph.NewResourceChainCommand())
	cmd.AddCommand(certsyncpod.NewCertSyncControllerCommand(operator.CertConfigMaps, operator.CertSecrets))
	cmd.AddCommand(certregenerationcontroller.NewCertRegenerationControllerCommand(ctx))
	cmd.AddCommand(insecurereadyz.NewInsecureReadyzCommand())
	cmd.AddCommand(checkendpoints.NewCheckEndpointsCommand())
	cmd.AddCommand(startupmonitor.NewCommand(startupmonitorreadiness.New(), func(config *rest.Config) (operatorclientv1.KubeAPIServerInterface, error) {
		client, err := operatorclientv1.NewForConfig(config)
		if err != nil {
			return nil, err
		}
		return client.KubeAPIServers(), nil
	}))

	return cmd
}
