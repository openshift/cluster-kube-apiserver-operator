package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/audit"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/stretchr/testify/require"
)

var (
	expectedClusterCIDR = []string{"10.128.0.0/14"}
	expectedServiceCIDR = []string{"172.30.0.0/16"}
	clusterAPIConfig    = `
apiVersion: machine.openshift.io/v1beta1
kind: Cluster
metadata:
  creationTimestamp: null
  name: cluster
  namespace: openshift-machine-api
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
        - 10.128.0.0/14
    serviceDomain: ""
    services:
      cidrBlocks:
        - 172.30.0.0/16
  providerSpec: {}
status: {}
`
	networkConfig = `
apiVersion: config.openshift.io/v1
kind: Network
metadata:
  creationTimestamp: null
  name: cluster
spec:
  clusterNetwork:
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  networkType: OpenShiftSDN
  serviceNetwork:
    - 172.30.0.0/16
status: {}
`
	networkConfigV6 = `
apiVersion: config.openshift.io/v1
kind: Network
metadata:
  creationTimestamp: null
  name: cluster
spec:
  clusterNetwork:
    - cidr: fd01::/48
      hostPrefix: 64
  networkType: OpenShiftSDN
  serviceNetwork:
    - fd02::/112
status: {}
`
	networkConfigDual = `
apiVersion: config.openshift.io/v1
kind: Network
metadata:
  creationTimestamp: null
  name: cluster
spec:
  clusterNetwork:
    - cidr: fd01::/48
      hostPrefix: 64
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  networkType: OpenShiftSDN
  serviceNetwork:
    - fd02::/112
    - 172.30.0.0/16
status: {}
`
)

func TestDiscoverCIDRsFromNetwork(t *testing.T) {
	renderConfig := TemplateData{
		LockHostPath:   "",
		EtcdServerURLs: []string{""},
		EtcdServingCA:  "",
	}
	if err := discoverCIDRsFromNetwork([]byte(networkConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
	}
}

func TestDiscoverCIDRsFromClusterAPI(t *testing.T) {
	renderConfig := TemplateData{
		LockHostPath:   "",
		EtcdServerURLs: []string{""},
		EtcdServingCA:  "",
	}
	if err := discoverCIDRsFromClusterAPI([]byte(clusterAPIConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
	}
}

func TestDiscoverServiceAccountIssuer(t *testing.T) {
	tests := []struct {
		config string

		issuer string
	}{{
		config: `apiVersion: config.openshift.io/v1
kind: Authentication
metadata:
  name: cluster
spec: {}`,
	}, {
		config: `apiVersion: config.openshift.io/v1
kind: Authentication
metadata:
  name: cluster
spec:
  serviceAccountIssuer: https://test.dummy.url`,
		issuer: "https://test.dummy.url",
	}}
	for _, test := range tests {
		t.Run("", func(t *testing.T) {
			renderConfig := TemplateData{
				LockHostPath:   "",
				EtcdServerURLs: []string{""},
				EtcdServingCA:  "",
			}
			if err := discoverServiceAccountIssuer([]byte(test.config), &renderConfig); err != nil {
				t.Fatalf("failed to discoverServiceAccountIssuer: %v", err)
			}
			if !reflect.DeepEqual(renderConfig.ServiceAccountIssuer, test.issuer) {
				t.Fatalf("Got: %s, expected: %v", renderConfig.ServiceAccountIssuer, test.issuer)
			}
		})
	}
}

func TestDiscoverCIDRs(t *testing.T) {
	testCase := []struct {
		config []byte
	}{
		{
			config: []byte(networkConfig),
		},
		{
			config: []byte(clusterAPIConfig),
		},
	}

	for _, tc := range testCase {
		renderConfig := TemplateData{
			LockHostPath:   "",
			EtcdServerURLs: []string{""},
			EtcdServingCA:  "",
		}

		if err := discoverCIDRs(tc.config, &renderConfig); err != nil {
			t.Errorf("failed to discoverCIDRs: %v", err)
		}

		if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
		}
		if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
		}
	}
}

func TestRenderCommand(t *testing.T) {
	assetsInputDir, err := ioutil.TempDir("", "testdata")
	if err != nil {
		t.Errorf("unable to create assets input directory, error: %v", err)
	}
	templateDir := filepath.Join("..", "..", "..", "bindata", "bootkube")

	tempDisabledFeatureGates := configobservercontroller.FeatureBlacklist
	if tempDisabledFeatureGates == nil {
		tempDisabledFeatureGates = sets.NewString()
	}

	tests := []struct {
		// note the name is used as a name for a temporary directory
		name          string
		args          []string
		setupFunction func() error
		testFunction  func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error
	}{
		{
			name: "scenario 1 checks feature gates",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				actualGates, ok := cfg.APIServerArguments["feature-gates"]
				if !ok {
					return fmt.Errorf("missing \"feature-gates\" entry in APIServerArguments")
				}
				defaultFG, ok := configv1.FeatureSets[configv1.Default]
				if !ok {
					t.Fatalf("configv1.FeatureSets doesn't contain entries under %s (Default) key", configv1.Default)
				}
				expectedGates := []string{}
				for _, enabledFG := range defaultFG.Enabled {
					if tempDisabledFeatureGates.Has(enabledFG) {
						continue
					}
					expectedGates = append(expectedGates, fmt.Sprintf("%s=true", enabledFG))
				}
				for _, disabledFG := range defaultFG.Disabled {
					if tempDisabledFeatureGates.Has(disabledFG) {
						continue
					}
					expectedGates = append(expectedGates, fmt.Sprintf("%s=false", disabledFG))
				}
				if len(actualGates) != len(expectedGates) {
					return fmt.Errorf("expected to get exactly %d feature gates but found %d: expected=%v got=%v", len(expectedGates), len(actualGates), expectedGates, actualGates)
				}
				for _, actualGate := range actualGates {
					found := false
					for _, expectedGate := range expectedGates {
						if actualGate == expectedGate {
							found = true
							break
						}
					}

					if !found {
						return fmt.Errorf("%q not found on the list of expected feature gates %v", actualGate, expectedGates)
					}
				}
				return nil
			},
		},
		{
			name: "scenario 2 checks BindAddress under IPv6",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-config-file=" + filepath.Join(assetsInputDir, "config-v6.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "config-v6.yaml"), []byte(networkConfigV6), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if cfg.ServingInfo.BindAddress != "[::]:6443" {
					return fmt.Errorf("incorrect IPv6 BindAddress: %s", cfg.ServingInfo.BindAddress)
				}
				if cfg.ServingInfo.BindNetwork != "tcp6" {
					return fmt.Errorf("incorrect IPv6 BindNetwork: %s", cfg.ServingInfo.BindNetwork)
				}
				return nil
			},
		},
		{
			name: "scenario 3 checks BindAddress and ServicesSubnet under dual IPv4-IPv6",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-config-file=" + filepath.Join(assetsInputDir, "config-dual.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "config-dual.yaml"), []byte(networkConfigDual), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if cfg.ServingInfo.BindAddress != "0.0.0.0:6443" {
					return fmt.Errorf("incorrect dual-stack BindAddress: %s", cfg.ServingInfo.BindAddress)
				}
				if cfg.ServingInfo.BindNetwork != "tcp4" {
					return fmt.Errorf("incorrect dual-stack BindNetwork: %s", cfg.ServingInfo.BindNetwork)
				}
				if cfg.ServicesSubnet != "fd02::/112,172.30.0.0/16" {
					return fmt.Errorf("incorrect dual-stack ServicesSubnet: %s", cfg.ServicesSubnet)
				}
				return nil
			},
		},
		{
			name: "scenario 4 checks service account issuer when authentication no exists",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["service-account-issuer"]) > 0 {
					return fmt.Errorf("expected the service-account-issuer to be empty, but it was %s", cfg.APIServerArguments["service-account-issuer"])
				}
				return nil
			},
		},
		{
			name: "scenario 5 checks service account issuer when authentication exists but empty",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				data := ``
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "authentication.yaml"), []byte(data), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["service-account-issuer"]) > 0 {
					return fmt.Errorf("expected the service-account-issuer to be empty, but it was %s", cfg.APIServerArguments["service-account-issuer"])
				}
				return nil
			},
		},
		{
			name: "scenario 6 checks service account issuer when authentication exists but empty spec",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				data := `apiVersion: config.openshift.io/v1
kind: Authentication
metadata:
  name: cluster
spec: {}`
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "authentication.yaml"), []byte(data), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["service-account-issuer"]) > 0 {
					return fmt.Errorf("expected the service-account-issuer to be empty, but it was %s", cfg.APIServerArguments["service-account-issuer"])
				}
				return nil
			},
		},
		{
			name: "scenario 7 checks service account issuer when authentication spec has issuer set",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				data := `apiVersion: config.openshift.io/v1
kind: Authentication
metadata:
  name: cluster
spec:
  serviceAccountIssuer: https://test.dummy.url`
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "authentication.yaml"), []byte(data), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["service-account-issuer"]) == 0 {
					return fmt.Errorf("expected the service-account-issuer to be set, but it was empty")
				}
				if !reflect.DeepEqual(cfg.APIServerArguments["service-account-issuer"], kubecontrolplanev1.Arguments([]string{"https://test.dummy.url"})) {
					return fmt.Errorf("expected the service-account-issuer to be [ https://test.dummy.url ], but it was %s", cfg.APIServerArguments["service-account-issuer"])
				}
				return nil
			},
		},
	}

	for _, test := range tests {
		outDirName := strings.ReplaceAll(test.name, " ", "_")
		teardown, outputDir, err := setupAssetOutputDir(outDirName)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
		defer teardown()

		if test.setupFunction != nil {
			if err := test.setupFunction(); err != nil {
				t.Fatalf("%q failed to set up, error: %v", test.name, err)
			}
		}

		test.args = setOutputFlags(test.args, outputDir)
		err = runRender(test.args...)
		if err != nil {
			t.Fatalf("%s: got unexpected error %v", test.name, err)
		}

		rawConfigFile, err := ioutil.ReadFile(filepath.Join(outputDir, "configs", "config.yaml"))
		if err != nil {
			t.Fatalf("cannot read the rendered config file, error: %v", err)
		}

		configJson, err := yaml.YAMLToJSON(rawConfigFile)
		if err != nil {
			t.Fatalf("cannot transform the config file to JSON format, error: %v", err)
		}

		cfg := &kubecontrolplanev1.KubeAPIServerConfig{}
		if err := json.Unmarshal(configJson, cfg); err != nil {
			t.Fatalf("cannot unmarshal config into KubeAPIServerConfig, error: %v", err)
		}
		if test.testFunction != nil {
			if err := test.testFunction(cfg); err != nil {
				t.Fatalf("%q reports incorrect config file, error: %v", test.name, err)
			}
		}
	}
}

func TestGetDefaultConfigWithAuditPolicy(t *testing.T) {
	raw, err := getDefaultConfigWithAuditPolicy()
	require.NoError(t, err)
	require.True(t, len(raw) > 0)

	decoder := json.NewDecoder(bytes.NewBuffer(raw))
	config := map[string]interface{}{}
	err = decoder.Decode(&config)
	require.NoError(t, err)

	auditPolicyPathGot, _, err := unstructured.NestedStringSlice(config, "apiServerArguments", "audit-policy-file")
	require.NoError(t, err)
	require.Equal(t, []string{"openshift.local.audit/policy.yaml"}, auditPolicyPathGot)

	auditConfigEnabledGot, _, err := unstructured.NestedBool(config, "auditConfig", "enabled")
	require.NoError(t, err)
	require.True(t, auditConfigEnabledGot)

	auditConfigPolicyGot, _, err := unstructured.NestedMap(config, "auditConfig", "policyConfiguration")
	require.NoError(t, err)
	require.NotNil(t, auditConfigPolicyGot)

	defaultPolicy, err := audit.DefaultPolicy()
	require.NoError(t, err)
	policyExpected, err := convertToUnstructured(defaultPolicy)
	require.NoError(t, err)

	isEqual := equality.Semantic.DeepEqual(policyExpected, auditConfigPolicyGot)
	require.True(t, isEqual)
}

func setupAssetOutputDir(testName string) (teardown func(), outputDir string, err error) {
	outputDir, err = ioutil.TempDir("", testName)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "manifests"), os.ModePerm); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "configs"), os.ModePerm); err != nil {
		return nil, "", err
	}
	teardown = func() {
		os.RemoveAll(outputDir)
	}
	return
}

func setOutputFlags(args []string, dir string) []string {
	newArgs := []string{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--asset-output-dir=") {
			newArgs = append(newArgs, "--asset-output-dir="+filepath.Join(dir, "manifests"))
			continue
		}
		if strings.HasPrefix(arg, "--config-output-file=") {
			newArgs = append(newArgs, "--config-output-file="+filepath.Join(dir, "configs", "config.yaml"))
			continue
		}
		newArgs = append(newArgs, arg)
	}
	return newArgs
}

func runRender(args ...string) error {
	c := NewRenderCommand()
	os.Args = append([]string{""}, args...)
	return c.Execute()
}
