package reactionchain

import (
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

func NewOperatorChain() Resources {
	ret := &resourcesImpl{}

	payload := NewResource(NewCoordinates("", "Payload", "", "cluster"))
	ret.Add(payload)
	installer := NewResource(NewCoordinates("", "Installer", "", "cluster"))
	ret.Add(installer)

	cvo := NewResource(NewCoordinates("apps", "deployments", "openshift-cluster-version", "cluster-version")).
		Note(" - controller.CVO").
		From(payload)
	ret.Add(cvo)

	aggregatorClientRotationController := NewResource(NewCoordinates("apps", "deployments", operatorclient.OperatorNamespace, "openshift-kube-apiserver-operator")).
		Note(" - AggregatorClientRotation").
		From(cvo)
	ret.Add(aggregatorClientRotationController)

	installerProvidedAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-aggregator-client-ca")).
		Note(" - static").
		From(installer)
	ret.Add(installerProvidedAggregatorClientCA)

	operatorManagedAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.OperatorNamespace, "managed-aggregator-client-ca")).
		Note(" - rotated").
		From(aggregatorClientRotationController)
	ret.Add(operatorManagedAggregatorClientCA)

	kubeAPIServerAggregatorClientCAForPod := NewResource(NewCoordinates("", "configmaps", operatorclient.TargetNamespace, "aggregator-client-ca")).
		Note(" - unioned").
		From(installerProvidedAggregatorClientCA).
		From(operatorManagedAggregatorClientCA)
	ret.Add(kubeAPIServerAggregatorClientCAForPod)

	externalAggregatorClientCA := NewResource(NewCoordinates("", "configmaps", operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-aggregator-client-ca")).
		Note(" - synchronized").
		From(kubeAPIServerAggregatorClientCAForPod)
	ret.Add(externalAggregatorClientCA)

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
