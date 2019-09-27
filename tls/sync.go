package tls

var (
	// OpenShiftKubeAPIServer_EtcdServingCA is provided to kube-apiserver to identify etcd.
	OpenShiftKubeAPIServer_EtcdServingCA = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "etcd-serving-ca"},
		From:      &OpenShiftConfig_EtcdServingCA,
	}

	// OpenShiftKubeAPIServer_EtcdClientSecret is provided to kube-apiserver to authenticate to etcd.
	OpenShiftKubeAPIServer_EtcdClientSecret = SyncedSecret{
		Secret: Secret{"openshift-kube-apiserver", "etcd-serving-ca"},
		From:   &OpenShiftConfig_EtcdClient,
	}

	// OpenShiftKubeAPIServer_SATokenSigningCertsConfigMap holds the certs used to verify the SA token JWTs created by the kube-controller-manager-operator.
	OpenShiftKubeAPIServer_SATokenSigningCertsConfigMap = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "sa-token-signing-certs"},
		From:      &OpenShiftConfigManaged_SATokenSigningCerts,
	}

	// OpenShiftKubeAPIServer_AggregatorClientCAConfigMap contains certs to verify the aggregator.  We copy it from the shared location to here.
	OpenShiftKubeAPIServer_AggregatorClientCAConfigMap = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "aggregator-client-ca"},
		From:      &OpenShiftConfigManaged_KubeAPIServerAggregatedClientCA,
	}

	// OpenShiftKubeAPIServer_KubeletServingCAConfigMap allows us to verify the kubelet serving certs
	OpenShiftKubeAPIServer_KubeletServingCAConfigMap = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-kube-apiserver", "kubelet-serving-ca"},
		From:      &OpenShiftConfigManaged_CSRControllerCA,
	}

	// OpenShiftConfigManaged_KubeAPIServerClientCA contains certs used by the kube-apiserver to verify client certs.
	OpenShiftConfigManaged_KubeAPIServerClientCA = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-config-managed", "kube-apiserver-client-ca"},
		From:      &OpenShiftKubeAPIServer_ClientCA,
	}.Document()

	// OpenShiftConfigManaged_KubeletServingCA contains certs that can be used to verify a kubelet.
	OpenShiftConfigManaged_KubeletServingCA = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-config-managed", "kubelet-serving-ca"},
		From:      &OpenShiftKubeAPIServer_KubeletServingCAConfigMap,
	}.Document()

	// OpenShiftConfigManaged_KubeAPIServerServerCA contains certs that can be used to verify a kube-apiserver.
	OpenShiftConfigManaged_KubeAPIServerServerCA = SyncedConfigMap{
		ConfigMap: ConfigMap{"openshift-config-managed", "kube-apiserver-server-ca"},
		From:      &OpenShiftKubeAPIServer_KubeAPIServerServerCA,
	}.Document()
)
