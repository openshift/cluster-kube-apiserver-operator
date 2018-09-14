package render

import (
	"errors"

	"github.com/golang/glog"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/assets"
	"github.com/spf13/cobra"
)

type renderOpts struct {
	namespace            string
	image                string
	assetInputDir        string
	assetOutputDir       string
	manifestTemplatesDir string
	configDir            string
	cloudProviderDir     string
}

func NewRenderCommand() *cobra.Command {
	renderOpts := &renderOpts{}
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render kubernetes API server bootstrap manifests, secrets and configMaps",
		Run: func(cmd *cobra.Command, args []string) {
			if err := renderOpts.Validate(); err != nil {
				glog.Fatal(err)
			}
			if err := renderOpts.Run(); err != nil {
				glog.Fatal(err)
			}
		},
	}
	cmd.Flags().StringVar(&renderOpts.namespace, "namespace", "openshift-kube-apiserver", "Target namespace.")
	cmd.Flags().StringVar(&renderOpts.image, "image", "openshift/origin-hypershift:latest", "Image to use.")

	cmd.Flags().StringVar(&renderOpts.assetInputDir, "asset-output-dir", "", "Output path for rendered assets.")
	cmd.Flags().StringVar(&renderOpts.assetOutputDir, "asset-input-dir", "", "A path to directory with certificates and secrets.")
	cmd.Flags().StringVar(&renderOpts.manifestTemplatesDir, "manifest-templates-dir", "/usr/share/bootkube/manifests", "A path to directory with manifest templates.")

	cmd.Flags().StringVar(&renderOpts.configDir, "config-dir", "/etc/kubernetes/config", "A path to directory with a KubeApiServerConfig configuration file.")
	cmd.Flags().StringVar(&renderOpts.cloudProviderDir, "cloud-provider-dir", "/etc/kubernetes/cloud", "A path to directory with cloud provider configuration.")

	return cmd
}

func (r *renderOpts) Validate() error {
	if len(r.assetInputDir) == 0 {
		return errors.New("missing required flag: --asset-output-dir")
	}
	if len(r.assetOutputDir) == 0 {
		return errors.New("missing required flag: --asset-input-dir")
	}
	if len(r.manifestTemplatesDir) == 0 {
		return errors.New("missing required flag: --manifest-templates-dir")
	}
	return nil
}

func (r *renderOpts) Run() error {
	defaultAssetsConfig := assets.Config{
		Namespace:           r.namespace,
		Image:               r.image,
		ImagePullPolicy:     "IfNotPresent",
		KubeAPIServerConfig: assets.KubeAPIServerConfig{},
		ConfigDir:           r.configDir,
		CloudProviderDir:    r.cloudProviderDir,
	}

	// Generate the kubernetes secrets
	defaultAssetsConfig.Secrets = assets.LoadLocalSecrets(r.assetOutputDir)
	defaultAssetsConfig.Secrets.Namespace = defaultAssetsConfig.Namespace

	secretAssets := assets.NewSecretStaticAssets(r.manifestTemplatesDir, defaultAssetsConfig)
	if err := secretAssets.WriteFiles(r.assetInputDir); err != nil {
		return err
	}

	// Generate the kubernetes config maps
	defaultAssetsConfig.ConfigMaps = assets.LoadLocalConfigMaps(r.assetOutputDir)
	defaultAssetsConfig.ConfigMaps.Namespace = defaultAssetsConfig.Namespace

	configAssets := assets.NewConfigStaticAssets(r.manifestTemplatesDir, defaultAssetsConfig)
	if err := configAssets.WriteFiles(r.assetInputDir); err != nil {
		return err
	}

	// Generate the kubernetes manifests
	kubeAssets := assets.NewKubernetesStaticAssets(r.manifestTemplatesDir, defaultAssetsConfig)
	if err := kubeAssets.WriteFiles(r.assetInputDir); err != nil {
		return err
	}

	return nil
}
