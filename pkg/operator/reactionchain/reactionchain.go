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

	payload := NewResource(NewCoordinates("", "Payload", "", "cluster")).
		Add(ret)
	installer := NewResource(NewCoordinates("", "Installer", "", "cluster")).
		Add(ret)
	user := NewResource(NewCoordinates("", "User", "", "cluster")).
		Add(ret)

	cvo := NewOperator("cluster-version").
		From(payload).
		Add(ret)
	kasOperator := NewOperator("kube-apiserver").
		From(cvo).
		Add(ret)
	kcmOperator := NewOperator("kube-controller-manager").
		From(cvo).
		Add(ret)
	authenticationOperator := NewOperator("authentication").
		From(cvo).
		Add(ret)
	imageRegistryOperator := NewOperator("image-registry").
		From(cvo).
		Add(ret)
	networkOperator := NewOperator("network").
		From(cvo).
		Add(ret)

	// config.openshift.io
	apiserverConfig := NewConfig("apiservers").
		From(user).
		Add(ret)
	userClientCA := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-client-ca>").
		Note("User").
		From(user).
		From(apiserverConfig).
		Add(ret)
	userDefaultServing := NewSecret(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-default-serving>").
		Note("User").
		From(user).
		From(apiserverConfig).
		Add(ret)
	authenticationConfig := NewConfig("authentications").
		From(user).
		From(authenticationOperator).
		Add(ret)
	userWellKnown := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-well-known>").
		Note("User").
		From(user).
		From(authenticationConfig).
		Add(ret)
	managedWellKnown := NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "openshift-authentication").
		Note("Managed").
		From(authenticationOperator).
		From(authenticationConfig).
		Add(ret)
	imageConfig := NewConfig("images").
		From(user).
		From(imageRegistryOperator).
		Add(ret)
	networkConfig := NewConfig("network").
		From(user).
		From(networkOperator).
		Add(ret)

	// aggregator client
	initialAggregatorCA := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-aggregator-client-ca").
		Note("Static").
		From(installer).
		Add(ret)
	aggregatorSigner := NewSecret(operatorclient.OperatorNamespace, "aggregator-client-signer").
		Note("Rotated").
		From(kasOperator).
		Add(ret)
	aggregatorClient := NewSecret(operatorclient.TargetNamespace, "aggregator-client").
		Note("Rotated").
		From(aggregatorSigner).
		Add(ret)
	operatorManagedAggregatorClientCA := NewConfigMap(operatorclient.OperatorNamespace, "managed-aggregator-client-ca").
		Note("Rotated").
		From(aggregatorSigner).
		Add(ret)
	kasAggregatorClientCAForPod := NewConfigMap(operatorclient.TargetNamespace, "aggregator-client-ca").
		Note("Unioned").
		From(initialAggregatorCA).
		From(operatorManagedAggregatorClientCA).
		Add(ret)
	// this is a destination and consumed by OAS
	_ = NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-aggregator-client-ca").
		Note("Synchronized").
		From(kasAggregatorClientCAForPod).
		Add(ret)

	// client CAs
	initialClientCA := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-client-ca").
		Note("Static").
		From(installer).
		Add(ret)
	kcmControllerCSRCA := NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "csr-controller-ca").
		Note("Synchronized").
		From(kcmOperator).
		Add(ret)
	// TODO this appears to be dead
	_ = NewConfigMap(operatorclient.OperatorNamespace, "csr-controller-ca").
		Note("Synchronized").
		From(kcmControllerCSRCA).
		Add(ret)
	managedClientSigner := NewSecret(operatorclient.OperatorNamespace, "managed-kube-apiserver-client-signer").
		Note("Rotated").
		From(kasOperator).
		Add(ret)
	_ = NewSecret(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-controller-manager-client-cert-key").
		Note("Rotated").
		From(managedClientSigner).
		Add(ret)
	_ = NewSecret(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-scheduler-client-cert-key").
		Note("Rotated").
		From(managedClientSigner).
		Add(ret)
	managedClientCA := NewConfigMap(operatorclient.OperatorNamespace, "managed-kube-apiserver-client-ca-bundle").
		Note("Rotated").
		From(managedClientSigner).
		Add(ret)
	clientCA := NewConfigMap(operatorclient.TargetNamespace, "client-ca").
		Note("Unioned").
		From(initialClientCA).
		From(kcmControllerCSRCA).
		From(managedClientCA).
		From(userClientCA).
		Add(ret)
	// this is a destination and consumed by OAS
	_ = NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-client-ca").
		Note("Synchronized").
		From(clientCA).
		Add(ret)

	// etcd certs
	fromEtcdServingCA := NewConfigMap("kube-system", "etcd-serving-ca").
		Note("Static").
		From(installer).
		Add(ret)
	fromEtcdClient := NewSecret("kube-system", "etcd-client").
		Note("Static").
		From(installer).
		Add(ret)
	etcdServingCA := NewConfigMap(operatorclient.TargetNamespace, "etcd-serving-ca").
		Note("Synchronized").
		From(fromEtcdServingCA).
		Add(ret)
	etcdClient := NewSecret(operatorclient.TargetNamespace, "etcd-client").
		Note("Synchronized").
		From(fromEtcdClient).
		Add(ret)

	// kubelet client
	initialKubeletClient := NewSecret(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-kubelet-client").
		Note("Static").
		From(installer).
		Add(ret)
	kubeletClient := NewSecret(operatorclient.TargetNamespace, "kubelet-client").
		Note("Synchronized").
		From(initialKubeletClient).
		Add(ret)

	// kubelet serving
	// TODO this is just a courtesy for the pod team
	intialKubeletServingCA := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-kubelet-serving-ca").
		Note("Static").
		From(installer).
		Add(ret)
	kubeletServingCA := NewConfigMap(operatorclient.TargetNamespace, "kubelet-serving-ca").
		Note("Unioned").
		From(intialKubeletServingCA).
		From(kcmControllerCSRCA).
		Add(ret)
	// this is a destination for things like monitoring to get a kubelet serving CA
	_ = NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kubelet-serving-ca").
		Note("Synchroinized").
		From(kubeletServingCA).
		Add(ret)

	// sa token verification
	initialSATokenPub := NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-sa-token-signing-certs").
		Note("Static").
		From(installer).
		Add(ret)
	mountedInitialSATokenPub := NewConfigMap(operatorclient.TargetNamespace, "initial-sa-token-signing-certs").
		Note("Synchronized").
		From(initialSATokenPub).
		Add(ret)
	kcmSATokenPub := NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "sa-token-signing-certs").
		Note("Static").
		From(installer).
		Add(ret)
	mountedKCMSATokenPub := NewConfigMap(operatorclient.TargetNamespace, "kube-controller-manager-sa-token-signing-certs").
		Note("Synchronized").
		From(kcmSATokenPub).
		Add(ret)

	// well_known
	wellKnown := NewConfigMap(operatorclient.TargetNamespace, "oauth-metadata").
		Note("PickOne").
		From(userWellKnown).
		From(managedWellKnown).
		Add(ret)

	// observedConfig
	config := NewConfigMap(operatorclient.OperatorNamespace, "config").
		Note("Managed").
		From(apiserverConfig).          // to specify client-ca, default serving, sni serving
		From(authenticationConfig).     // to specify well_known
		From(imageConfig).              // to specify internal and external registries and trust
		From(mountedInitialSATokenPub). // to choose which SA token files are used
		From(mountedKCMSATokenPub).     // to choose which SA token files are used
		From(networkConfig).            // to choose which SA token files are used
		Add(ret)

	// and finally our target pod
	_ = NewResource(NewCoordinates("", "pods", operatorclient.TargetNamespace, "kube-apiserver")).
		From(kasAggregatorClientCAForPod).
		From(aggregatorClient).
		From(clientCA).
		From(config).
		From(etcdServingCA).
		From(etcdClient).
		From(kubeletClient).
		From(kubeletServingCA).
		From(mountedInitialSATokenPub).
		From(mountedKCMSATokenPub).
		From(userDefaultServing).
		From(wellKnown).
		Add(ret)

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
	case n.Resource.Coordinates().Group == "config.openshift.io":
		color = `"#c7bfff"` // purple
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
