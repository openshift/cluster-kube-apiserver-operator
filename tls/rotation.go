package tls

import (
	"time"
)

var (
	kubeControlPlaneSignerConfig = SignerConfig{
		Namespace: "openshift-kube-apiserver-operator", Name: "kube-control-plane-signer",
		Validity: 60 * 24 * time.Hour, // this comes from the installer TODO: what does this mean?
		Refresh:  30 * 24 * time.Hour, // this means we effectively do not rotate
	}
	kubeControlPlaneSignerCA = CABundleConfig{"openshift-kube-apiserver-operator", "kube-control-plane-signer-ca"}

	// OpenShiftConfigManaged_KubeControllerManagerClientCertKey is the client certificate to be used by kube-controller-manager
	// to talk to kube-apiserver.
	OpenShiftConfigManaged_KubeControllerManagerClientCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-config-managed", "kube-controller-manager-client-cert-key"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer:   kubeControlPlaneSignerConfig,
		CABundle: kubeControlPlaneSignerCA,
	}.Document()

	// OpenShiftConfigManaged_KubeSchedulerClientCertKey is the client certificate to be used by the kube-scheduler
	// to talk to kube-apiserver.
	OpenShiftConfigManaged_KubeSchedulerClientCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-config-managed", "kube-scheduler-client-cert-key"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer:   kubeControlPlaneSignerConfig,
		CABundle: kubeControlPlaneSignerCA,
	}.Document()

	// OpenShiftConfigManaged_KubeAPIServerCertSyncerClientCertKey is ????
	// TODO: document
	OpenShiftConfigManaged_KubeAPIServerCertSyncerClientCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "kube-apiserver-cert-syncer-client-cert-key"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer:   kubeControlPlaneSignerConfig,
		CABundle: kubeControlPlaneSignerCA,
	}

	// OpenShiftKubeAPIServerOperator_KubeControlPlaneSigner is used to sign everything between kube-apiserver,
	// kube-controller-manager and kube-scheduler.
	OpenShiftKubeAPIServerOperator_KubeControlPlaneSigner = RotatedSigner{[]*RotatedCertificate{
		&OpenShiftConfigManaged_KubeControllerManagerClientCertKey,
		&OpenShiftConfigManaged_KubeSchedulerClientCertKey,
		&OpenShiftConfigManaged_KubeAPIServerCertSyncerClientCertKey,
	}}
	// OpenShiftKubeAPIServerOperator_KubeControlPlaneSignerCA is used to identify all control plane component
	// to each-other.
	OpenShiftKubeAPIServerOperator_KubeControlPlaneSignerCA = RotatedCABundle{[]*RotatedCertificate{
		&OpenShiftConfigManaged_KubeControllerManagerClientCertKey,
		&OpenShiftConfigManaged_KubeSchedulerClientCertKey,
		&OpenShiftConfigManaged_KubeAPIServerCertSyncerClientCertKey,
	}}

	// OpenShiftKubeAPIServer_AggregatorClient is the client certificate used by the aggregator
	// inside kube-apiserver to identify as aggregator to other components like
	// aggregated API servers.
	OpenShiftKubeAPIServer_AggregatorClient = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "aggregator-client"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer: SignerConfig{
			Namespace: "openshift-kube-apiserver-operator", Name: "aggregator-client-signer",
			Validity: 30 * 24 * time.Hour,
			Refresh:  15 * 24 * time.Hour,
		},
		CABundle: CABundleConfig{"openshift-config-managed", "kube-apiserver-aggregator-client-ca"},
	}.Document()
	OpenShiftKubeAPIServerOperator_AggregatorClientSigner  = RotatedSigner{[]*RotatedCertificate{&OpenShiftKubeAPIServer_AggregatorClient}}
	OpenShiftConfigManaged_KubeAPIServerAggregatedClientCA = RotatedCABundle{[]*RotatedCertificate{&OpenShiftKubeAPIServer_AggregatorClient}}

	// OpenShiftKubeAPIServerOperator_KubeAPIServerToKubeletClientCA is from the
	// installer and contains the value to verify the kube-apiserver communicating to the kubelet

	// OpenShiftKubeAPIServer_KubeletClient is the client certificate for kube-apiserver
	// to identify as kube-apiserver to the kubelet.
	OpenShiftKubeAPIServer_KubeletClient = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "kubelet-client"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer: SignerConfig{Namespace: "openshift-kube-apiserver-operator", Name: "kube-apiserver-to-kubelet-signer",
			Validity: 1 * 365 * 24 * time.Hour, // this comes from the installer TODO: what does this mean?
			Refresh:  8 * 365 * 24 * time.Hour, // this means we effectively do not rotate
		},
		CABundle: CABundleConfig{"openshift-kube-apiserver-operator", "kube-apiserver-to-kubelet-client-ca"},
	}.Document()
	OpenShiftKubeAPIServerOperator_KubeAPIServerToKubeletSigner   = RotatedSigner{[]*RotatedCertificate{&OpenShiftKubeAPIServer_KubeletClient}}
	OpenShiftKubeAPIServerOperator_KubeAPIServerToKubeletClientCA = ConfigMap{"openshift-kube-apiserver-operator", "kube-apiserver-to-kubelet-client-ca"}

	// OpenShiftKubeAPIServerOperator_LocalhostServingCA contains the CA to verify the identity
	// of kupe-apiserver on the localhost interface.

	// OpenShiftKubeAPIServer_LocalhostServingCertCertKey contains the kube-apiserver serving cert/key for
	// listening on localhost.
	OpenShiftKubeAPIServer_LocalhostServingCertCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "localhost-serving-cert-certkey"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer: SignerConfig{
			Namespace: "openshift-kube-apiserver-operator",
			Name:      "localhost-serving-signer",
			Validity:  10 * 365 * 24 * time.Hour, // this comes from the installer TODO: what does this mean?
			Refresh:   8 * 365 * 24 * time.Hour,  // this means we effectively do not rotate
		},
		CABundle: CABundleConfig{"openshift-kube-apiserver-operator", "localhost-serving-ca"},
	}
	OpenShiftKubeAPIServerOperator_LocalhostSigner    = RotatedSigner{[]*RotatedCertificate{&OpenShiftKubeAPIServer_LocalhostServingCertCertKey}}
	OpenShiftKubeAPIServerOperator_LocalhostServingCA = RotatedCABundle{[]*RotatedCertificate{&OpenShiftKubeAPIServer_LocalhostServingCertCertKey}}

	// OpenShiftKubeAPIServer_ServingNetworkServingCertKey is the serving cert for the kube-apiserver presented to
	// clients on the Kubernetes service network.
	OpenShiftKubeAPIServer_ServingNetworkServingCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "service-network-serving-certkey"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer: SignerConfig{
			Namespace: "openshift-kube-apiserver-operator",
			Name:      "service-network-serving-signer",
			Validity:  10 * 365 * 24 * time.Hour, // this comes from the installer TODO: what does this mean?
			Refresh:   8 * 365 * 24 * time.Hour,  // this means we effectively do not rotate
		},
		CABundle: CABundleConfig{"openshift-kube-apiserver-operator", "service-network-serving-ca"},
	}
	OpenShiftKubeAPIServerOperator_ServiceNetworkSigner    = RotatedSigner{[]*RotatedCertificate{&OpenShiftKubeAPIServer_ServingNetworkServingCertKey}}
	OpenShiftKubeAPIServerOperator_ServiceNetworkServingCA = RotatedCABundle{[]*RotatedCertificate{&OpenShiftKubeAPIServer_ServingNetworkServingCertKey}}

	loadBalancerSignerConfig = SignerConfig{Namespace: "openshift-kube-apiserver", Name: "loadbalancer-serving-signer",
		Validity: 10 * 365 * 24 * time.Hour, // this comes from the installer TODO: what does this mean?
		Refresh:  8 * 365 * 24 * time.Hour,  // this means we effectively do not rotate
	}
	loadBalancerCABundleConfig = CABundleConfig{"openshift-kube-apiserver", "loadbalancer-serving-ca"}

	// OpenShiftKubeAPIServer_ExternalLoadBalancerServingCertKey is the serving cert for the kube-apiserver presented to
	// clients coming through the load balancers accessible to the internet.
	OpenShiftKubeAPIServer_ExternalLoadBalancerServingCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "external-loadbalancer-serving-certkey"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer:   loadBalancerSignerConfig,
		CABundle: loadBalancerCABundleConfig,
	}

	// OpenShiftKubeAPIServer_InternalLoadBalancerServingCertKey is the serving cert for the kube-apiserver presented to
	// clients coming through the load balancers accessible from within the cluster, e.g.
	// kubelets.
	OpenShiftKubeAPIServer_InternalLoadBalancerServingCertKey = RotatedCertificate{
		Secret:   Secret{"openshift-kube-apiserver", "internal-loadbalancer-serving-certkey"},
		Validity: 30 * 24 * time.Hour,
		Refresh:  15 * 24 * time.Hour,
		Signer:   loadBalancerSignerConfig,
		CABundle: loadBalancerCABundleConfig,
	}

	OpenShiftKubeAPIServer_LoadBalancerServingSigner = RotatedSigner{[]*RotatedCertificate{&OpenShiftKubeAPIServer_ExternalLoadBalancerServingCertKey}}
	OpenShiftKubeAPIServer_LoadBalancerServingCA     = RotatedCABundle{[]*RotatedCertificate{&OpenShiftKubeAPIServer_ExternalLoadBalancerServingCertKey}}

	// OpenShiftKubeAPIServerOperator_CSRSigner contains the cert/key we use to sign CSRs.
	OpenShiftKubeAPIServerOperator_CSRSigner = Secret{Namespace: "openshift-kube-apiserver-operator", Name: "csr-signer"}

	// OpenShiftKubeAPIServerOperator_CSRSignerCA contains the CA we use to sign CSRs.
	OpenShiftKubeAPIServerOperator_CSRSignerCA = ConfigMap{Namespace: "openshift-kube-apiserver-operator", Name: "csr-signer-ca"}
)
