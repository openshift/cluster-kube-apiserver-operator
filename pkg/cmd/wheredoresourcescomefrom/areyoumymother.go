package wheredoresourcescomefrom

import (
	"fmt"
	"strings"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/reactionchain"
	"github.com/spf13/cobra"
)

func NewResourceChainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "where-do-resources-come-from",
		Short: "ask your mother",
		Run: func(cmd *cobra.Command, args []string) {
			chain := reactionchain.NewOperatorChain()
			resource := chain.Resource(reactionchain.ResourceCoordinates{Group: "", Resource: "configmaps", Namespace: "openshift-config-managed", Name: "kube-apiserver-aggregator-client-ca"})
			fmt.Println(strings.Join(resource.DumpSources(0), "\n"))
		},
	}

	return cmd
}
