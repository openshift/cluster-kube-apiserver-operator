package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeAPIServer provides information to configure an operator to manage kube-apiserver.
//
// Compatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).
// +openshift:compatibility-gen:level=1
// +openshift:compatibility-gen:level=1
type KubeAPIServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// spec is the specification of the desired behavior of the Kubernetes API Server
	// +kubebuilder:validation:Required
	// +required
	Spec KubeAPIServerSpec `json:"spec"`

	// status is the most recently observed status of the Kubernetes API Server
	// +optional
	Status KubeAPIServerStatus `json:"status"`
}

type KubeAPIServerSpec struct {
	StaticPodOperatorSpec `json:",inline"`
}

type KubeAPIServerStatus struct {
	StaticPodOperatorStatus `json:",inline"`

	// serviceAccountIssuers tracks history of used service account issuers.
	// The item without expiration time represents the currently used service account issuer.
	// The other items represents service account issuers that were used previously and are still being trusted.
	// see: https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-token-volume-projection
	// +optional
	ServiceAccountIssuers []ServiceAccountIssuerStatus `json:"serviceAccountIssuers,omitempty"`
}

type ServiceAccountIssuerStatus struct {
	// name is the name of the service account issuer
	Name string `json:"string"`

	// expirationTime is the time after which this service account issuer will be pruned and removed from the trusted list
	// of service account issuers.
	// +optional
	ExpirationTime *metav1.Time `json:"expirationTime,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeAPIServerList is a collection of items
//
// Compatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).
// +openshift:compatibility-gen:level=1
type KubeAPIServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	// Items contains the items
	Items []KubeAPIServer `json:"items"`
}
