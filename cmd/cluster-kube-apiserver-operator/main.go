package main

import (
	goflag "flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	utilflag "k8s.io/apiserver/pkg/util/flag"
	"k8s.io/apiserver/pkg/util/logs"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/operator"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/render"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/prune"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	command := NewOperatorCommand()
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func NewOperatorCommand() *cobra.Command {
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

	cmd.AddCommand(operator.NewOperator())
	cmd.AddCommand(render.NewRenderCommand())
	cmd.AddCommand(installerpod.NewInstaller())
	cmd.AddCommand(prune.NewPrune())

	return cmd
}
