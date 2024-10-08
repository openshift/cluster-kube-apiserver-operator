// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// NodeDisruptionPolicySpecSSHKeyApplyConfiguration represents a declarative configuration of the NodeDisruptionPolicySpecSSHKey type for use
// with apply.
type NodeDisruptionPolicySpecSSHKeyApplyConfiguration struct {
	Actions []NodeDisruptionPolicySpecActionApplyConfiguration `json:"actions,omitempty"`
}

// NodeDisruptionPolicySpecSSHKeyApplyConfiguration constructs a declarative configuration of the NodeDisruptionPolicySpecSSHKey type for use with
// apply.
func NodeDisruptionPolicySpecSSHKey() *NodeDisruptionPolicySpecSSHKeyApplyConfiguration {
	return &NodeDisruptionPolicySpecSSHKeyApplyConfiguration{}
}

// WithActions adds the given value to the Actions field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Actions field.
func (b *NodeDisruptionPolicySpecSSHKeyApplyConfiguration) WithActions(values ...*NodeDisruptionPolicySpecActionApplyConfiguration) *NodeDisruptionPolicySpecSSHKeyApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithActions")
		}
		b.Actions = append(b.Actions, *values[i])
	}
	return b
}
