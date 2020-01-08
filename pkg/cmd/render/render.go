package render

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"
	genericrender "github.com/openshift/library-go/pkg/operator/render"
	genericrenderoptions "github.com/openshift/library-go/pkg/operator/render/options"
)

const (
	bootstrapVersion = "v4.1.0"
)

// renderOpts holds values to drive the render command.
type renderOpts struct {
	manifest genericrenderoptions.ManifestOptions
	generic  genericrenderoptions.GenericOptions

	lockHostPath      string
	etcdServerURLs    []string
	etcdServingCA     string
	clusterConfigFile string
}

// NewRenderCommand creates a render command.
func NewRenderCommand() *cobra.Command {
	renderOpts := renderOpts{
		generic:  *genericrenderoptions.NewGenericOptions(),
		manifest: *genericrenderoptions.NewManifestOptions("kube-apiserver", "openshift/origin-hyperkube:latest"),

		lockHostPath:   "/var/run/kubernetes/lock",
		etcdServerURLs: []string{"https://127.0.0.1:2379"},
		etcdServingCA:  "root-ca.crt",
	}
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render kubernetes API server bootstrap manifests, secrets and configMaps",
		Run: func(cmd *cobra.Command, args []string) {
			if err := renderOpts.Validate(); err != nil {
				klog.Fatal(err)
			}
			if err := renderOpts.Complete(); err != nil {
				klog.Fatal(err)
			}
			if err := renderOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	renderOpts.AddFlags(cmd.Flags())

	return cmd
}

func (r *renderOpts) AddFlags(fs *pflag.FlagSet) {
	r.manifest.AddFlags(fs, "apiserver")
	r.generic.AddFlags(fs, kubecontrolplanev1.GroupVersion.WithKind("KubeAPIServerConfig"))

	fs.StringVar(&r.lockHostPath, "manifest-lock-host-path", r.lockHostPath, "A host path mounted into the apiserver pods to hold lock.")
	fs.StringArrayVar(&r.etcdServerURLs, "manifest-etcd-server-urls", r.etcdServerURLs, "The etcd server URL, comma separated.")
	fs.StringVar(&r.etcdServingCA, "manifest-etcd-serving-ca", r.etcdServingCA, "The etcd serving CA.")
	fs.StringVar(&r.clusterConfigFile, "cluster-config-file", r.clusterConfigFile, "Openshift Cluster API Config file.")
}

// Validate verifies the inputs.
func (r *renderOpts) Validate() error {
	if err := r.manifest.Validate(); err != nil {
		return err
	}
	if err := r.generic.Validate(); err != nil {
		return err
	}

	// TODO: enable check when the installer is updated
	//if len(r.manifest.OperatorImage) == 0 {
	//	return errors.New("missing required flag: --manifest-operator-image")
	//}
	if len(r.lockHostPath) == 0 {
		return errors.New("missing required flag: --manifest-lock-host-path")
	}
	if len(r.etcdServerURLs) == 0 {
		return errors.New("missing etcd server URLs: --manifest-etcd-server-urls")
	}
	if len(r.etcdServingCA) == 0 {
		return errors.New("missing etcd serving CA: --manifest-etcd-serving-ca")
	}

	return nil
}

// Complete fills in missing values before command execution.
func (r *renderOpts) Complete() error {
	if err := r.manifest.Complete(); err != nil {
		return err
	}
	if err := r.generic.Complete(); err != nil {
		return err
	}
	return nil
}

type TemplateData struct {
	genericrenderoptions.ManifestConfig
	genericrenderoptions.FileConfig

	// LockHostPath holds the api server lock file for bootstrap
	LockHostPath string

	// EtcdServerURLs is a list of etcd server URLs.
	EtcdServerURLs []string

	// EtcdServingCA is the serving CA used by the etcd servers.
	EtcdServingCA string

	// ClusterCIDR is the IP range for pod IPs.
	ClusterCIDR []string

	// ServiceClusterIPRange is the IP range for service IPs.
	ServiceCIDR []string

	// BindAddress is the IP address and port to bind to
	BindAddress string

	// BindNetwork is the network (tcp4 or tcp6) to bind to
	BindNetwork string
}

// Run contains the logic of the render command.
func (r *renderOpts) Run() error {
	renderConfig := TemplateData{
		LockHostPath:   r.lockHostPath,
		EtcdServerURLs: r.etcdServerURLs,
		EtcdServingCA:  r.etcdServingCA,
		BindAddress:    "0.0.0.0:6443",
		BindNetwork:    "tcp4",
	}
	if len(r.clusterConfigFile) > 0 {
		clusterConfigFileData, err := ioutil.ReadFile(r.clusterConfigFile)
		if err != nil {
			return err
		}
		if err = discoverCIDRs(clusterConfigFileData, &renderConfig); err != nil {
			return fmt.Errorf("unable to parse restricted CIDRs from config %q: %v", r.clusterConfigFile, err)
		}
	}
	if len(renderConfig.ClusterCIDR) > 0 {
		anyIPv4 := false
		for _, cidr := range renderConfig.ClusterCIDR {
			cidrBaseIP, _, err := net.ParseCIDR(cidr)
			if err != nil {
				return fmt.Errorf("invalid cluster CIDR %q: %v", cidr, err)
			}
			if cidrBaseIP.To4() != nil {
				anyIPv4 = true
				break
			}
		}
		if !anyIPv4 {
			// Single-stack IPv6 cluster, so listen on IPv6 not IPv4.
			renderConfig.BindAddress = "[::]:6443"
			renderConfig.BindNetwork = "tcp6"
		}
	}
	if err := r.manifest.ApplyTo(&renderConfig.ManifestConfig); err != nil {
		return err
	}

	if err := r.generic.ApplyTo(
		&renderConfig.FileConfig,
		genericrenderoptions.Template{FileName: "defaultconfig.yaml", Content: v410_00_assets.MustAsset(filepath.Join(bootstrapVersion, "kube-apiserver", "defaultconfig.yaml"))},
		mustReadTemplateFile(filepath.Join(r.generic.TemplatesDir, "config", "bootstrap-config-overrides.yaml")),
		mustReadTemplateFile(filepath.Join(r.generic.TemplatesDir, "config", "config-overrides.yaml")),
		&renderConfig,
		nil,
	); err != nil {
		return err
	}

	return genericrender.WriteFiles(&r.generic, &renderConfig.FileConfig, renderConfig)
}

func mustReadTemplateFile(fname string) genericrenderoptions.Template {
	bs, err := ioutil.ReadFile(fname)
	if err != nil {
		panic(fmt.Sprintf("Failed to load %q: %v", fname, err))
	}
	return genericrenderoptions.Template{FileName: fname, Content: bs}
}

func discoverCIDRs(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	if err := discoverCIDRsFromNetwork(clusterConfigFileData, renderConfig); err != nil {
		if err = discoverCIDRsFromClusterAPI(clusterConfigFileData, renderConfig); err != nil {
			return err
		}
	}
	return nil
}

func discoverCIDRsFromNetwork(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	configJson, err := yaml.YAMLToJSON(clusterConfigFileData)
	if err != nil {
		return err
	}
	clusterConfigObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, configJson)
	if err != nil {
		return err
	}
	clusterConfig, ok := clusterConfigObj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected object in %t", clusterConfigObj)
	}
	clusterCIDR, found, err := unstructured.NestedSlice(
		clusterConfig.Object, "spec", "clusterNetwork")
	if found && err == nil {
		for key := range clusterCIDR {
			slice, ok := clusterCIDR[key].(map[string]interface{})
			if !ok {
				return fmt.Errorf("unexpected object in %t", clusterCIDR[key])
			}
			if CIDR, found, err := unstructured.NestedString(slice, "cidr"); found && err == nil {
				renderConfig.ClusterCIDR = append(renderConfig.ClusterCIDR, CIDR)
			}
		}
	}
	if err != nil {
		return err
	}
	serviceCIDR, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "serviceNetwork")
	if found && err == nil {
		renderConfig.ServiceCIDR = serviceCIDR
	}
	if err != nil {
		return err
	}
	return nil
}

func discoverCIDRsFromClusterAPI(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	configJson, err := yaml.YAMLToJSON(clusterConfigFileData)
	if err != nil {
		return err
	}
	clusterConfigObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, configJson)
	if err != nil {
		return err
	}
	clusterConfig, ok := clusterConfigObj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected object in %t", clusterConfigObj)
	}
	clusterCIDR, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "clusterNetwork", "pods", "cidrBlocks")
	if found && err == nil {
		renderConfig.ClusterCIDR = clusterCIDR
	}
	if err != nil {
		return err
	}
	serviceCIDR, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "clusterNetwork", "services", "cidrBlocks")
	if found && err == nil {
		renderConfig.ServiceCIDR = serviceCIDR
	}
	if err != nil {
		return err
	}
	return nil
}
