package assets

type Config struct {
	KubeAPIServerConfig

	// ConfigDir is the directory where the "config.json" must be located
	ConfigDir string

	// CloudProviderDir is the directory with cloud provider specific data
	CloudProviderDir string

	// Namespace is the target namespace for the bootstrap kubeapi server to be created
	Namespace string

	// Image is the pull spec of the image to use for the api server.
	Image string

	// ImagePullPolicy specifies the image pull policy to use for the images.
	ImagePullPolicy string
}

type KubeAPIServerConfig struct {
	Secrets    KubeAPIServerSecretsConfig
	ConfigMaps KubeAPIServerConfigMapsConfig
}

type KubeAPIServerSecretsConfig struct {
	Namespace string

	AggregatorClientCertCrt []byte
	AggregatorClientCertKey []byte

	KubeletClientCertCrt []byte
	KubeletClientCertKey []byte

	ServingCertCrt []byte
	ServingCertKey []byte

	EtcdClientCertCrt []byte
	EtcdClientCertKey []byte
}

type KubeAPIServerConfigMapsConfig struct {
	Namespace string

	SATokenSigningCerts []byte
	AggregatorClientCA  []byte
	KubeletServingCA    []byte
	ClientCA            []byte
	EtcdServingCA       []byte
}
