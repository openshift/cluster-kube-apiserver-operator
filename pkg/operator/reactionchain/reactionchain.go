package reactionchain

import (
	"fmt"
	"strings"

	"github.com/gonum/graph"
	"github.com/gonum/graph/encoding/dot"
	"github.com/gonum/graph/simple"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

func NewOperatorChain() Resources {
	ret := &resourcesImpl{}

	payload := NewResource(NewCoordinates("", "Payload", "", "cluster"))
	ret.Add(payload)
	installer := NewResource(NewCoordinates("", "Installer", "", "cluster"))
	ret.Add(installer)

	cvo := NewResource(NewCoordinates("config.openshift.io", "clusteroperators", "", "cluster-version")).
		From(payload)
	ret.Add(cvo)

	kasOperator := NewResource(NewCoordinates("config.openshift.io", "clusteroperators", "", "kube-apiserver")).
		From(cvo)
	ret.Add(kasOperator)

	kcmOperator := NewResource(NewCoordinates("config.openshift.io", "clusteroperators", "", "kube-controller-manager")).
		From(cvo)
	ret.Add(kcmOperator)

	installerProvidedAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-aggregator-client-ca")).
		Note("Static").
		From(installer)
	ret.Add(installerProvidedAggregatorClientCA)

	operatorManagedAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.OperatorNamespace, "managed-aggregator-client-ca")).
		Note("Rotated").
		From(kasOperator)
	ret.Add(operatorManagedAggregatorClientCA)

	kubeAPIServerAggregatorClientCAForPod := NewResource(NewCoordinates("", "configmaps", operatorclient.TargetNamespace, "aggregator-client-ca")).
		Note("Unioned").
		From(installerProvidedAggregatorClientCA).
		From(operatorManagedAggregatorClientCA)
	ret.Add(kubeAPIServerAggregatorClientCAForPod)

	externalAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-aggregator-client-ca")).
		Note("Synchronized").
		From(kubeAPIServerAggregatorClientCAForPod)
	ret.Add(externalAggregatorClientCA)

	initialClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-client-ca")).
		Note("Static").
		From(installer)
	ret.Add(initialClientCA)

	csrControllerCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalMachineSpecifiedConfigNamespace, "csr-controller-ca")).
		Note("Managed").
		From(kcmOperator)
	ret.Add(csrControllerCA)

	managedClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.OperatorNamespace, "managed-kube-apiserver-client-ca-bundle")).
		Note("Rotated").
		From(kasOperator)
	ret.Add(managedClientCA)

	clientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.TargetNamespace, "client-ca")).
		Note("Unioned").
		From(initialClientCA).
		From(csrControllerCA).
		From(managedClientCA)
	ret.Add(clientCA)

	kas := NewResource(NewCoordinates("", "pods", operatorclient.TargetNamespace, "kube-apiserver")).
		From(kubeAPIServerAggregatorClientCAForPod).
		From(clientCA)
	ret.Add(kas)

	return ret
}

type resourcesImpl struct {
	resources []Resource
}

func (r *resourcesImpl) Add(resource Resource) {
	r.resources = append(r.resources, resource)
}

func (r *resourcesImpl) Dump() []string {
	lines := []string{}
	for _, root := range r.Roots() {
		lines = append(lines, root.Dump(0)...)
	}
	return lines
}

func (r *resourcesImpl) AllResources() []Resource {
	ret := []Resource{}
	for _, v := range r.resources {
		ret = append(ret, v)
	}
	return ret
}

func (r *resourcesImpl) Resource(coordinates ResourceCoordinates) Resource {
	for _, v := range r.resources {
		if v.Coordinates() == coordinates {
			return v
		}
	}
	return nil
}

func (r *resourcesImpl) Roots() []Resource {
	ret := []Resource{}
	for _, resource := range r.AllResources() {
		if len(resource.Sources()) > 0 {
			continue
		}
		ret = append(ret, resource)
	}
	return ret
}

type resourceGraphNode struct {
	simple.Node
	Resource Resource
}

// DOTAttributes implements an attribute getter for the DOT encoding
func (n resourceGraphNode) DOTAttributes() []dot.Attribute {
	color := "white"
	switch {
	case n.Resource.Coordinates().Resource == "clusteroperators":
		color = `"#c8fbcd"` // green
	case n.Resource.Coordinates().Resource == "configmaps":
		color = `"#bdebfd"` // blue
	case n.Resource.Coordinates().Resource == "secrets":
		color = `"#fffdb8"` // yellow
	case n.Resource.Coordinates().Resource == "pods":
		color = `"#ffbfb8"` // red
	}
	resource := n.Resource.Coordinates().Resource
	if len(n.Resource.Coordinates().Group) > 0 {
		resource = resource + "." + n.Resource.Coordinates().Group
	}
	label := fmt.Sprintf("%s\n%s\n%s\n%s", resource, n.Resource.Coordinates().Name, n.Resource.Coordinates().Namespace, n.Resource.GetNote())
	return []dot.Attribute{
		{Key: "label", Value: fmt.Sprintf("%q", label)},
		{Key: "style", Value: "filled"},
		{Key: "fillcolor", Value: color},
	}
}

func (r *resourcesImpl) NewGraph() graph.Directed {
	g := simple.NewDirectedGraph(1.0, 0.0)

	coordinatesToNode := map[ResourceCoordinates]graph.Node{}
	idToCoordinates := map[int]ResourceCoordinates{}

	// make all nodes
	allResources := r.AllResources()
	for i := range allResources {
		resource := allResources[i]
		id := g.NewNodeID()
		node := resourceGraphNode{Node: simple.Node(id), Resource: resource}

		coordinatesToNode[resource.Coordinates()] = node
		idToCoordinates[id] = resource.Coordinates()
		g.AddNode(node)
	}

	// make all edges
	for i := range allResources {
		resource := allResources[i]

		for _, source := range resource.Sources() {
			from := coordinatesToNode[source.Coordinates()]
			to := coordinatesToNode[resource.Coordinates()]
			g.SetEdge(simple.Edge{F: from, T: to})
		}
	}

	return g
}

// Quote takes an arbitrary DOT ID and escapes any quotes that is contains.
// The resulting string is quoted again to guarantee that it is a valid ID.
// DOT graph IDs can be any double-quoted string
// See http://www.graphviz.org/doc/info/lang.html
func Quote(id string) string {
	return fmt.Sprintf(`"%s"`, strings.Replace(id, `"`, `\"`, -1))
}
