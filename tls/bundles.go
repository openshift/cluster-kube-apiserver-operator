package tls

var (
	// TODO: document
	OpenShiftKubeAPIServerOperator_CSRControllerCA = CombinedCABundle{
		ConfigMap: ConfigMap{Namespace: "openshift-kube-apiserver-operator", Name: "csr-controller-ca"},
		From: []ConfigMapReference{
			&OpenShiftKubeAPIServerOperator_CSRSignerCA,
			&OpenShiftKubeAPIServerOperator_CSRControllerSignerCA,
		},
	}.Document()

	// OpenShiftKubeAPIServer_ClientCA contains the CA to verify client cert for incoming requests to kube-apiserver.
	OpenShiftKubeAPIServer_ClientCA = CombinedCABundle{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "client-ca"},
		From: []ConfigMapReference{
			&OpenShiftConfig_AdminKubeconfigClientCA,
			&OpenShiftConfigManaged_CSRControllerCA,
			&OpenShiftKubeAPIServerOperator_KubeAPIServerToKubeletClientCA,
			&OpenShiftKubeAPIServerOperator_KubeControlPlaneSignerCA,
			&OpenShiftKubeAPIServer_UserClientCA,
		},
	}.Document()

	// TODO: document
	OpenShiftKubeAPIServer_KubeAPIServerServerCA = CombinedCABundle{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "kube-apiserver-server-ca"},
		From: []ConfigMapReference{
			&OpenShiftKubeAPIServer_LoadBalancerServingCA,
			&OpenShiftKubeAPIServerOperator_LocalhostServingCA,
			&OpenShiftKubeAPIServerOperator_ServiceNetworkServingCA,
		},
	}.Document()
)
