package recoveryapiserver

// Options are shared by other subcommands
type Options struct {
	PodManifestDir string
}

func NewDefaultOptions() Options {
	return Options{
		PodManifestDir: "/etc/kubernetes/manifests",
	}
}

func (o *Options) Complete() error {
	return nil
}

func (o *Options) Validate() error {
	return nil
}
