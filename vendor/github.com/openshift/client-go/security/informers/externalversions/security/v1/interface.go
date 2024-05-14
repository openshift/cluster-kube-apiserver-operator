// Code generated by informer-gen. DO NOT EDIT.

package v1

import (
	internalinterfaces "github.com/openshift/client-go/security/informers/externalversions/internalinterfaces"
)

// Interface provides access to all the informers in this group version.
type Interface interface {
	// RangeAllocations returns a RangeAllocationInformer.
	RangeAllocations() RangeAllocationInformer
	// SecurityContextConstraints returns a SecurityContextConstraintsInformer.
	SecurityContextConstraints() SecurityContextConstraintsInformer
}

type version struct {
	factory          internalinterfaces.SharedInformerFactory
	namespace        string
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// New returns a new Interface.
func New(f internalinterfaces.SharedInformerFactory, namespace string, tweakListOptions internalinterfaces.TweakListOptionsFunc) Interface {
	return &version{factory: f, namespace: namespace, tweakListOptions: tweakListOptions}
}

// RangeAllocations returns a RangeAllocationInformer.
func (v *version) RangeAllocations() RangeAllocationInformer {
	return &rangeAllocationInformer{factory: v.factory, tweakListOptions: v.tweakListOptions}
}

// SecurityContextConstraints returns a SecurityContextConstraintsInformer.
func (v *version) SecurityContextConstraints() SecurityContextConstraintsInformer {
	return &securityContextConstraintsInformer{factory: v.factory, tweakListOptions: v.tweakListOptions}
}
