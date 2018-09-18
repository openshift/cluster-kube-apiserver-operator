package render

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/assets"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
)

const (
	bootstrapVersion = "v3.11.0"
)

// manifestOpts holds values to parametrize the manifests
type manifestOpts struct {
	namespace             string
	image                 string
	imagePullPolicy       string
	configHostPath        string
	configFileName        string
	cloudProviderHostPath string
}

// renderOpts holds values to drive the render command.
type renderOpts struct {
	manifest manifestOpts

	templatesDir       string
	assetInputDir      string
	assetOutputDir     string
	configOverrideFile string
	configOutputFile   string
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

	cmd.Flags().StringVar(&renderOpts.manifest.namespace, "manifest-namespace", "openshift-kube-apiserver", "Target namespace for API server pods.")
	cmd.Flags().StringVar(&renderOpts.manifest.image, "manifest-image", "openshift/origin-hypershift:latest", "Image to use for the API server.")
	cmd.Flags().StringVar(&renderOpts.manifest.imagePullPolicy, "manifest-image-pull-policy", "IfNotPresent", "Image pull policy to use for the API server.")
	cmd.Flags().StringVar(&renderOpts.manifest.configHostPath, "manifest-config-host-path", "/etc/kubernetes/config", "A host path mounted into the apiserver pods to hold a config file.")
	cmd.Flags().StringVar(&renderOpts.manifest.configFileName, "manifest-config-file-name", "kube-apiserver-config.yaml", "The config file name inside the manifest-config-host-path.")
	cmd.Flags().StringVar(&renderOpts.manifest.cloudProviderHostPath, "manifest-cloud-provider-host-path", "/etc/kubernetes/cloud", "A host path mounted into the apiserver pods to hold cloud provider configuration.")

	cmd.Flags().StringVar(&renderOpts.assetOutputDir, "asset-output-dir", "", "Output path for rendered manifests.")
	cmd.Flags().StringVar(&renderOpts.assetInputDir, "asset-input-dir", "", "A path to directory with certificates and secrets.")
	cmd.Flags().StringVar(&renderOpts.templatesDir, "templates-input-dir", "/usr/share/bootkube/manifests", "A path to a directory with manifest templates.")
	cmd.Flags().StringVar(&renderOpts.configOverrideFile, "config-override-file", "", "A sparse KubeAPIConfig.kubecontrolplane.config.openshift.io/v1 file (default: kube-apiserver-config-overrides.yaml in the asset-input-dir)")
	cmd.Flags().StringVar(&renderOpts.configOutputFile, "config-output-file", "", "Output path for the KubeAPIServerConfig yaml file.")

	return cmd
}

func (r *renderOpts) Validate() error {
	if len(r.manifest.namespace) == 0 {
		return errors.New("missing required flag: --manifest-namespace")
	}
	if len(r.manifest.image) == 0 {
		return errors.New("missing required flag: --manifest-image")
	}
	if len(r.manifest.imagePullPolicy) == 0 {
		return errors.New("missing required flag: --manifest-image-pull-policy")
	}
	if len(r.manifest.configHostPath) == 0 {
		return errors.New("missing required flag: --manifest-config-host-path")
	}
	if len(r.manifest.configFileName) == 0 {
		return errors.New("missing required flag: --manifest-config-file-name")
	}
	if len(r.manifest.cloudProviderHostPath) == 0 {
		return errors.New("missing required flag: --manifest-cloud-provider-host-path")
	}

	if len(r.assetInputDir) == 0 {
		return errors.New("missing required flag: --asset-output-dir")
	}
	if len(r.assetOutputDir) == 0 {
		return errors.New("missing required flag: --asset-input-dir")
	}
	if len(r.templatesDir) == 0 {
		return errors.New("missing required flag: --templates-dir")
	}
	if len(r.configOutputFile) == 0 {
		return errors.New("missing required flag: --config-output-file")
	}

	return nil
}

func (r *renderOpts) complete() error {
	if len(r.configOverrideFile) == 0 {
		r.configOverrideFile = filepath.Join(r.assetInputDir, "kube-apiserver-config-overrides.yaml")
	}

	return nil
}

func (r *renderOpts) Run() error {
	if err := r.complete(); err != nil {
		return err
	}

	assetsConfig := assets.Config{
		Namespace:             r.manifest.namespace,
		Image:                 r.manifest.image,
		ImagePullPolicy:       r.manifest.imagePullPolicy,
		KubeAPIServerConfig:   assets.KubeAPIServerConfig{},
		ConfigHostPath:        r.manifest.configHostPath,
		ConfigFileName:        r.manifest.configFileName,
		CloudProviderHostPath: r.manifest.cloudProviderHostPath,
	}

	// Create configuration from defaultconfig.yaml and optional overrides
	mergedConfig, err := r.configFromDefaultsPlusOverride(filepath.Join(r.templatesDir, "bootstrap-manifests", "config-overrides.yaml"))
	if err != nil {
		return fmt.Errorf("failed to generated bootstrap config: %v", err)
	}
	if err := ioutil.WriteFile(r.configOutputFile, mergedConfig, 644); err != nil {
		return fmt.Errorf("failed to write merged config to %q: %v", r.configOutputFile, err)
	}

	// Generate the kubernetes secrets
	assetsConfig.Secrets = assets.LoadLocalSecrets(r.assetInputDir)
	assetsConfig.Secrets.Namespace = assetsConfig.Namespace

	secretAssets := assets.NewSecretStaticAssets(r.templatesDir, assetsConfig)
	if err := secretAssets.WriteFiles(r.assetOutputDir); err != nil {
		return err
	}

	// Generate the kubernetes config maps
	assetsConfig.ConfigMaps = assets.LoadLocalConfigMaps(r.assetInputDir)
	assetsConfig.ConfigMaps.Namespace = assetsConfig.Namespace
	assetsConfig.ConfigMaps.KubeAPIServerConfig, err = r.configFromDefaultsPlusOverride(filepath.Join(r.templatesDir, "manifests", "config-overrides.yaml"))

	configAssets := assets.NewConfigStaticAssets(r.templatesDir, assetsConfig)
	if err := configAssets.WriteFiles(r.assetOutputDir); err != nil {
		return err
	}

	// Generate the kubernetes manifests
	kubeAssets := assets.NewKubernetesStaticAssets(r.templatesDir, assetsConfig)
	if err := kubeAssets.WriteFiles(r.assetOutputDir); err != nil {
		return err
	}

	return nil
}

func (r *renderOpts) configFromDefaultsPlusOverride(tlsOverride string) ([]byte, error) {
	defaultConfig := v311_00_assets.MustAsset(filepath.Join(bootstrapVersion, "kube-apiserver", "defaultconfig.yaml"))
	bootstrapOverrides, err := ioutil.ReadFile(tlsOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to load config override file %q: %v", tlsOverride, err)
	}
	configs := [][]byte{defaultConfig, bootstrapOverrides}
	if len(r.configOverrideFile) > 0 {
		overrides, err := ioutil.ReadFile(r.configOverrideFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load config overrides at %q: %v", r.configOverrideFile, err)
		}
		configs = append(configs, overrides)
	}
	mergedConfig, err := resourcemerge.MergeProcessConfig(nil, configs...)
	if err != nil {
		return nil, fmt.Errorf("failed to merge configs: %v", err)
	}
	yml, err := yaml.JSONToYAML(mergedConfig)
	if err != nil {
		return nil, err
	}

	return yml, nil
}
