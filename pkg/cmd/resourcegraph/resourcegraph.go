package resourcegraph

import (
	"fmt"

	"github.com/golang/glog"
	"github.com/gonum/graph/encoding/dot"
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/reactionchain"
)

func NewResourceChainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource-graph",
		Short: "Where do resources come from? Ask your mother.",
		Run: func(cmd *cobra.Command, args []string) {
			chain := reactionchain.NewOperatorChain()
			g := chain.NewGraph()

			data, err := dot.Marshal(g, reactionchain.Quote("kube-apiserver-operator"), "", "  ", false)
			if err != nil {
				glog.Fatal(err)
			}
			fmt.Println(string(data))
		},
	}

	return cmd
}
