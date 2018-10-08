package render

type Config struct {
	// ConfigHostPath is a host path mounted into the apiserver pods to hold the config file.
	ConfigHostPath string

	// ConfigFileName is the filename of config file inside ConfigHostPath.
	ConfigFileName string

	// CloudProviderHostPath is a host path mounted into the apiserver pods to hold cloud provider configuration.
	CloudProviderHostPath string

	// SecretsHostPath holds certs and keys
	SecretsHostPath string

	// LockHostPath holds the api server lock file for bootstrap
	LockHostPath string

	// EtcdServerURLs is a list of etcd server URLs.
	EtcdServerURLs []string

	// MasterIPAddress is an ip address pointing to the master0 host.
	MasterIPAddress string

	// Namespace is the target namespace for the bootstrap kubeapi server to be created.
	Namespace string

	// Image is the pull spec of the image to use for the api server.
	Image string

	// ImagePullPolicy specifies the image pull policy to use for the images.
	ImagePullPolicy string

	// PostBootstrapKubeAPIServerConfig holds the rendered kube-apiserver config file after bootstrapping.
	PostBootstrapKubeAPIServerConfig []byte

	// Assets holds the loaded assets like certs and keys.
	Assets map[string][]byte
}
