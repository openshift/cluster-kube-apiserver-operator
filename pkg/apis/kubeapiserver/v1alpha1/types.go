package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	operatorsv1alpha1api "github.com/openshift/api/operator/v1alpha1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeAPISConfig provides information to configure kube-apiserver
type KubeAPIServerConfig struct {
	metav1.TypeMeta `json:",inline"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeAPISOperatorConfig provides information to configure an operator to manage kube-apiserver.
type KubeAPIServerOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   KubeAPIServerOperatorConfigSpec   `json:"spec"`
	Status KubeAPIServerOperatorConfigStatus `json:"status"`
}

type KubeAPIServerOperatorConfigSpec struct {
	operatorsv1alpha1api.OperatorSpec `json:",inline"`

	// userConfig holds a sparse config that the user wants for this component.  It only needs to be the overrides from the defaults
	// it will end up overlaying in the following order:
	// 1. hardcoded default
	// 2. this config
	UserConfig runtime.RawExtension `json:"userConfig"`

	// observedConfig holds a sparse config that controller has observed from the cluster state.  It exists in spec because
	// it causes action for the operator
	ObservedConfig runtime.RawExtension `json:"observedConfig"`
}

type KubeAPIServerOperatorConfigStatus struct {
	operatorsv1alpha1api.OperatorStatus `json:",inline"`

	// latestDeploymentID is the deploymentID of the most recent deployment
	LatestDeploymentID int32 `json:"latestDeploymentID"`

	TargetKubeletStates []KubeletState `json:"kubeletStates"`
}

type KubeletState struct {
	NodeName string `json:"nodeName"`

	// currentDeploymentID is the ID of the most recently successful deployment
	CurrentDeploymentID int32 `json:"currentDeploymentID"`
	// targetDeploymentID is the ID of the deployment we're trying to apply
	TargetDeploymentID int32 `json:"targetDeploymentID"`
	// lastFailedDeploymentID is the ID of the deployment we tried and failed to deploy.
	LastFailedDeploymentID int32 `json:"lastFailedDeploymentID"`

	// errors is a list of the errors during the deployment installation
	Errors []string `json:"errors"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeAPISOperatorConfigList is a collection of items
type KubeAPIServerOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the items
	Items []KubeAPIServerOperatorConfig `json:"items"`
}
