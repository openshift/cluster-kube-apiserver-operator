package tls

var (
	// OpenShiftConfig_EtcdServingCA is provided to kube-apiserver to identify etcd.
	OpenShiftConfig_EtcdServingCA = InputConfigMap{
		ConfigMap:  ConfigMap{"openshift-config", "etcd-serving-ca"},
		ProvidedBy: "etcd-operator",
	}

	// OpenShiftConfig_EtcdClient is provided to kube-apiserver to authenticate to etcd.
	OpenShiftConfig_EtcdClient = InputSecret{
		Secret:     Secret{"openshift-config", "etcd-client"},
		ProvidedBy: "etcd-operator",
	}

	// TODO: document
	OpenShiftConfigManaged_SATokenSigningCerts = InputConfigMap{
		ConfigMap:  ConfigMap{"openshift-config-managed", "sa-token-signing-certs"},
		ProvidedBy: "installer",
	}

	// OpenShiftConfig_AdminKubeconfigClientCA contains the admin user client cert
	// from the installer.
	OpenShiftConfig_AdminKubeconfigClientCA = InputConfigMap{
		ConfigMap:  ConfigMap{Namespace: "openshift-config", Name: "admin-kubeconfig-client-ca"},
		ProvidedBy: "installer",
	}

	// OpenShiftConfigManaged_CSRControllerCA is from the installer and contains
	// the value to verify the node bootstrapping cert that is baked into images
	// this is from kube-controller-manager and indicates the ca-bundle.crt to
	// verify their signatures (kubelet client certs).
	OpenShiftConfigManaged_CSRControllerCA = InputConfigMap{
		ConfigMap:  ConfigMap{"openshift-config-managed", "csr-controller-ca"},
		ProvidedBy: "cluster-kube-controller-manager-operator",
	}

	// OpenShiftKubeAPIServerOperator_CSRControllerSignerCA contains the CA we use
	// to sign the cert key pairs from from csr-signer.
	OpenShiftKubeAPIServerOperator_CSRControllerSignerCA = InputConfigMap{
		ConfigMap:  ConfigMap{Namespace: "openshift-kube-apiserver-operator", Name: "csr-controller-signer-ca"},
		ProvidedBy: "???",
	}

	// TODO: document
	OpenShiftKubeAPIServer_UserClientCA = InputConfigMap{
		ConfigMap:  ConfigMap{Namespace: "openshift-kube-apiserver", Name: "user-client-ca"},
		ProvidedBy: "???",
	}
)
