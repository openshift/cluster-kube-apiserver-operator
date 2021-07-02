package render

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"
	libgoaudit "github.com/openshift/library-go/pkg/operator/apiserver/audit"
	genericrender "github.com/openshift/library-go/pkg/operator/render"
	genericrenderoptions "github.com/openshift/library-go/pkg/operator/render/options"

	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	clusterAuthFile   string
	infraConfigFile   string
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
	fs.StringVar(&r.clusterAuthFile, "cluster-auth-file", r.clusterAuthFile, "Openshift Cluster Authentication API Config file.")
	fs.StringVar(&r.infraConfigFile, "infra-config-file", "", "File containing infrastructure.config.openshift.io manifest.")
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

	if err := validateBoundSATokensSigningKeys(r.generic.AssetInputDir); err != nil {
		return err
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

	// TerminationGracePeriodSeconds is set in pod manifest
	TerminationGracePeriodSeconds int

	// ShutdownDelayDuration is passed to kube-apiserver. Empty means not to override defaultconfig's value.
	ShutdownDelayDuration string

	ServiceAccountIssuer string
}

// Run contains the logic of the render command.
func (r *renderOpts) Run() error {
	renderConfig := TemplateData{
		LockHostPath:                  r.lockHostPath,
		EtcdServerURLs:                r.etcdServerURLs,
		EtcdServingCA:                 r.etcdServingCA,
		BindAddress:                   "0.0.0.0:6443",
		BindNetwork:                   "tcp4",
		TerminationGracePeriodSeconds: 135, // bit more than 70s (minimal termination period) + 60s (apiserver graceful termination)
		ShutdownDelayDuration:         "",  // do not override
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
	if len(r.clusterAuthFile) > 0 {
		clusterAuthFileData, err := ioutil.ReadFile(r.clusterAuthFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to load authentication config: %v", err)
		}
		if len(clusterAuthFileData) > 0 {
			if err := discoverServiceAccountIssuer(clusterAuthFileData, &renderConfig); err != nil {
				return fmt.Errorf("unable to parse service-account issuers from config %q: %v", r.clusterAuthFile, err)
			}
		}
	}

	boundSAPublicPath := filepath.Join(r.generic.AssetInputDir, "bound-service-account-signing-key.pub")
	boundSAPrivatePath := filepath.Join(r.generic.AssetInputDir, "bound-service-account-signing-key.key")
	_, privStatErr := os.Stat(boundSAPrivatePath)
	if privStatErr != nil {
		if !os.IsNotExist(privStatErr) {
			return fmt.Errorf("failed to access %s: %v", boundSAPrivatePath, privStatErr)
		}

		// the private key is missing => generate the keypair
		pubPEM, privPEM, err := generateKeyPairPEM()
		if err != nil {
			return fmt.Errorf("failed to generate an RSA keypair for bound SA token signing: %v", err)
		}

		if err := ioutil.WriteFile(boundSAPrivatePath, privPEM, os.FileMode(0600)); err != nil {
			return fmt.Errorf("failed to write private key for bound SA token signing: %v", err)
		}

		if err := ioutil.WriteFile(boundSAPublicPath, pubPEM, os.FileMode(0644)); err != nil {
			return fmt.Errorf("failed to write public key for bound SA token verification: %v", err)
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

	if len(r.infraConfigFile) > 0 {
		infra, err := getInfrastructure(r.infraConfigFile)
		if err != nil {
			return fmt.Errorf("failed to get infrastructure config: %w", err)
		}

		switch infra.Status.ControlPlaneTopology {
		case configv1.SingleReplicaTopologyMode:
			renderConfig.TerminationGracePeriodSeconds = 15
			renderConfig.ShutdownDelayDuration = "0s"
		}
	}

	if err := r.manifest.ApplyTo(&renderConfig.ManifestConfig); err != nil {
		return err
	}

	defaultConfig, err := getDefaultConfigWithAuditPolicy()
	if err != nil {
		return fmt.Errorf("failed to get default config with audit policy - %s", err)
	}

	if err := r.generic.ApplyTo(
		&renderConfig.FileConfig,
		genericrenderoptions.Template{FileName: "defaultconfig.yaml", Content: defaultConfig},
		mustReadTemplateFile(filepath.Join(r.generic.TemplatesDir, "config", "bootstrap-config-overrides.yaml")),
		&renderConfig,
		nil,
	); err != nil {
		return err
	}

	return genericrender.WriteFiles(&r.generic, &renderConfig.FileConfig, renderConfig)
}

func getDefaultConfigWithAuditPolicy() ([]byte, error) {
	rawBytes, err := libgoaudit.DefaultPolicy()
	if err != nil {
		return nil, fmt.Errorf("failed to get default audit policy - %s", err)
	}

	rawPolicyJSON, err := kyaml.ToJSON(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset yaml to JSON - %w", err)
	}

	policy, err := convertToUnstructured(rawPolicyJSON)
	asset := filepath.Join(bootstrapVersion, "config", "defaultconfig.yaml")
	raw, err := v410_00_assets.Asset(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to get default config asset asset=%s - %s", asset, err)
	}

	rawJSON, err := kyaml.ToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset yaml to JSON asset=%s - %s", asset, err)
	}

	defaultConfig, err := convertToUnstructured(rawJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to decode default config into unstructured - %s", err)
	}

	if err := addAuditPolicyToConfig(defaultConfig, policy); err != nil {
		return nil, fmt.Errorf("failed to add audit policy into default config - %s", err)
	}

	defaultConfigRaw, err := json.Marshal(defaultConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal default config - %s", err)
	}

	return defaultConfigRaw, nil
}

func addAuditPolicyToConfig(config, policy map[string]interface{}) error {
	const (
		auditConfigPath  = "auditConfig"
		localAuditPolicy = "openshift.local.audit/policy.yaml"
	)

	auditConfigEnabledPath := []string{auditConfigPath, "enabled"}
	if err := unstructured.SetNestedField(config, true, auditConfigEnabledPath...); err != nil {
		return fmt.Errorf("failed to set audit configuration field=%s - %s", auditConfigEnabledPath, err)
	}

	auditConfigPolicyConfigurationPath := []string{auditConfigPath, "policyConfiguration"}
	if err := unstructured.SetNestedMap(config, policy, auditConfigPolicyConfigurationPath...); err != nil {
		return fmt.Errorf("failed to set audit configuration field=%s - %s", auditConfigPolicyConfigurationPath, err)
	}

	apiServerArgumentsAuditPath := []string{"apiServerArguments", "audit-policy-file"}
	if err := unstructured.SetNestedStringSlice(config, []string{localAuditPolicy}, apiServerArgumentsAuditPath...); err != nil {
		return fmt.Errorf("failed to set audit configuration field=%s - %s", apiServerArgumentsAuditPath, err)
	}

	return nil
}

func convertToUnstructured(raw []byte) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewBuffer(raw))
	u := map[string]interface{}{}
	if err := decoder.Decode(&u); err != nil {
		return nil, err
	}

	return u, nil
}

func mustReadTemplateFile(fname string) genericrenderoptions.Template {
	bs, err := ioutil.ReadFile(fname)
	if err != nil {
		panic(fmt.Sprintf("Failed to load %q: %v", fname, err))
	}
	return genericrenderoptions.Template{FileName: fname, Content: bs}
}

func discoverServiceAccountIssuer(clusterAuthFileData []byte, renderConfig *TemplateData) error {
	configJson, err := yaml.YAMLToJSON(clusterAuthFileData)
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
	issuer, found, err := unstructured.NestedString(
		clusterConfig.Object, "spec", "serviceAccountIssuer")
	if found && err == nil {
		renderConfig.ServiceAccountIssuer = issuer
	}
	return err
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

func validateBoundSATokensSigningKeys(assetsDir string) error {
	boundSAPublicPath := filepath.Join(assetsDir, "bound-service-account-signing-key.pub")
	boundSAPrivatePath := filepath.Join(assetsDir, "bound-service-account-signing-key.key")
	_, pubStatErr := os.Stat(boundSAPublicPath)
	_, privStatErr := os.Stat(boundSAPrivatePath)

	if pubStatErr != nil {
		if !os.IsNotExist(pubStatErr) {
			return fmt.Errorf("failed to access %s: %v", boundSAPublicPath, pubStatErr)
		} else if privStatErr == nil {
			return fmt.Errorf("%s was supplied, but the matching public key is missing", boundSAPrivatePath)
		}
	}

	if privStatErr != nil {
		if !os.IsNotExist(privStatErr) {
			return fmt.Errorf("failed to access %s: %v", boundSAPrivatePath, privStatErr)
		} else if pubStatErr == nil {
			return fmt.Errorf("%s was supplied, but the matching private key is missing", boundSAPublicPath)
		}
	}

	return nil
}

func generateKeyPairPEM() (pubKeyPEM []byte, privKeyPEM []byte, err error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	// convert the keys to PEM format
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode pub key: %v", err)
	}

	pubKeyPEM = pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: pubKeyBytes,
		},
	)

	privKeyBytes := x509.MarshalPKCS1PrivateKey(privKey)
	privKeyPEM = pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: privKeyBytes,
		},
	)

	return pubKeyPEM, privKeyPEM, nil
}

func getInfrastructure(file string) (*configv1.Infrastructure, error) {
	config := &configv1.Infrastructure{}
	yamlData, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	configJson, err := yaml.YAMLToJSON(yamlData)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(configJson, config)
	if err != nil {
		return nil, err
	}
	return config, nil
}
