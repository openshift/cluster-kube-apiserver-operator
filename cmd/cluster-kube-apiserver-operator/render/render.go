package render

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/assets"
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
	secretsHostPath       string
	lockHostPath          string
	etcdServerURLs        []string
	etcdServingCA         string
}

// renderOpts holds values to drive the render command.
type renderOpts struct {
	manifest manifestOpts

	templatesDir                 string
	assetInputDir                string
	assetOutputDir               string
	configOverrideFiles          []string
	deprecatedConfigOverrideFile string
	configOutputFile             string
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
	cmd.Flags().StringVar(&renderOpts.manifest.configHostPath, "manifest-config-host-path", "/etc/kubernetes/bootstrap-configs", "A host path mounted into the apiserver pods to hold a config file.")
	cmd.Flags().StringVar(&renderOpts.manifest.secretsHostPath, "manifest-secrets-host-path", "/etc/kubernetes/bootstrap-secrets", "A host path mounted into the apiserver pods to hold secrets.")
	cmd.Flags().StringVar(&renderOpts.manifest.lockHostPath, "manifest-lock-host-path", "/var/run/kubernetes/lock", "A host path mounted into the apiserver pods to hold lock.")
	cmd.Flags().StringVar(&renderOpts.manifest.configFileName, "manifest-config-file-name", "kube-apiserver-config.yaml", "The config file name inside the manifest-config-host-path.")
	cmd.Flags().StringVar(&renderOpts.manifest.cloudProviderHostPath, "manifest-cloud-provider-host-path", "/etc/kubernetes/cloud", "A host path mounted into the apiserver pods to hold cloud provider configuration.")
	cmd.Flags().StringArrayVar(&renderOpts.manifest.etcdServerURLs, "manifest-etcd-server-urls", []string{"https://127.0.0.1:2379"}, "The etcd server URL, comma separated.")
	cmd.Flags().StringVar(&renderOpts.manifest.etcdServingCA, "manifest-etcd-serving-ca", "root-ca.crt", "The etcd serving ca.")

	cmd.Flags().StringVar(&renderOpts.assetOutputDir, "asset-output-dir", "", "Output path for rendered manifests.")
	cmd.Flags().StringVar(&renderOpts.assetInputDir, "asset-input-dir", "", "A path to directory with certificates and secrets.")
	cmd.Flags().StringVar(&renderOpts.templatesDir, "templates-input-dir", "/usr/share/bootkube/manifests", "A path to a directory with manifest templates.")
	cmd.Flags().StringSliceVar(&renderOpts.configOverrideFiles, "config-override-files", nil, "Additional sparse KubeAPIConfig.kubecontrolplane.config.openshift.io/v1 files for customiziation through the installer, merged into the default config in the given order.")
	cmd.Flags().StringVar(&renderOpts.configOutputFile, "config-output-file", "", "Output path for the KubeAPIServerConfig yaml file.")

	// TODO: Remove these once we break the flag dependency loop in installer
	cmd.Flags().StringVar(&renderOpts.deprecatedConfigOverrideFile, "config-override-file", "", "")
	cmd.Flags().MarkHidden("config-override-file")
	cmd.Flags().MarkDeprecated("config-override-file", "Use 'config-override-files' flag instead")

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
	if len(r.manifest.secretsHostPath) == 0 {
		return errors.New("missing required flag: --manifest-secrets-host-path")
	}
	if len(r.manifest.lockHostPath) == 0 {
		return errors.New("missing required flag: --manifest-lock-host-path")
	}
	if len(r.manifest.etcdServerURLs) == 0 {
		return errors.New("missing etcd server URLs: --manifest-etcd-server-urls")
	}
	if len(r.manifest.etcdServingCA) == 0 {
		return errors.New("missing etcd serving CA: --manifest-etcd-serving-ca")
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
	return nil
}

func (r *renderOpts) Run() error {
	if err := r.complete(); err != nil {
		return err
	}

	renderConfig := Config{
		Namespace:             r.manifest.namespace,
		Image:                 r.manifest.image,
		ImagePullPolicy:       r.manifest.imagePullPolicy,
		ConfigHostPath:        r.manifest.configHostPath,
		ConfigFileName:        r.manifest.configFileName,
		CloudProviderHostPath: r.manifest.cloudProviderHostPath,
		SecretsHostPath:       r.manifest.secretsHostPath,
		LockHostPath:          r.manifest.lockHostPath,
		EtcdServerURLs:        r.manifest.etcdServerURLs,
		EtcdServingCA:         r.manifest.etcdServingCA,
	}

	// create post-poststrap configuration
	var err error
	renderConfig.PostBootstrapKubeAPIServerConfig, err = r.configFromDefaultsPlusOverride(&renderConfig, filepath.Join(r.templatesDir, "config", "config-overrides.yaml"))

	// load and render templates
	if renderConfig.Assets, err = assets.LoadFilesRecursively(r.assetInputDir); err != nil {
		return fmt.Errorf("failed loading assets from %q: %v", r.assetInputDir, err)
	}
	for _, manifestDir := range []string{"bootstrap-manifests", "manifests"} {
		manifests, err := assets.New(filepath.Join(r.templatesDir, manifestDir), renderConfig, assets.OnlyYaml)
		if err != nil {
			return fmt.Errorf("failed rendering assets: %v", err)
		}
		if err := manifests.WriteFiles(filepath.Join(r.assetOutputDir, manifestDir)); err != nil {
			return fmt.Errorf("failed writing assets to %q: %v", filepath.Join(r.assetOutputDir, manifestDir), err)
		}
	}

	// create bootstrap configuration
	mergedConfig, err := r.configFromDefaultsPlusOverride(&renderConfig, filepath.Join(r.templatesDir, "config", "bootstrap-config-overrides.yaml"))
	if err != nil {
		return fmt.Errorf("failed to generated bootstrap config: %v", err)
	}
	if err := ioutil.WriteFile(r.configOutputFile, mergedConfig, 0644); err != nil {
		return fmt.Errorf("failed to write merged config to %q: %v", r.configOutputFile, err)
	}

	return nil
}

func (r *renderOpts) configFromDefaultsPlusOverride(data *Config, tlsOverride string) ([]byte, error) {
	defaultConfig := v311_00_assets.MustAsset(filepath.Join(bootstrapVersion, "kube-apiserver", "defaultconfig.yaml"))
	bootstrapOverrides, err := readFileTemplate(tlsOverride, data)
	if err != nil {
		return nil, fmt.Errorf("failed to load config override file %q: %v", tlsOverride, err)
	}
	configs := [][]byte{defaultConfig, bootstrapOverrides}

	// TODO: Remove this when the flag is gone
	if len(r.deprecatedConfigOverrideFile) > 0 {
		r.configOverrideFiles = append(r.configOverrideFiles, r.deprecatedConfigOverrideFile)
	}
	for _, fname := range r.configOverrideFiles {
		overrides, err := readFileTemplate(fname, data)
		if err != nil {
			return nil, fmt.Errorf("failed to load config overrides at %q: %v", fname, err)
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

func readFileTemplate(fname string, data interface{}) ([]byte, error) {
	tpl, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, fmt.Errorf("failed to load %q: %v", fname, err)
	}

	tmpl, err := template.New(fname).Parse(string(tpl))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
