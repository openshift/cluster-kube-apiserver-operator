package render

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ghodss/yaml"

	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
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
					expectedGates = append(expectedGates, fmt.Sprintf("%s=true", enabledFG))
				}
				for _, disabledFG := range defaultFG.Disabled {
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
			name: "scenario 3 checks BindAddress under dual IPv4-IPv6",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-config-file=" + filepath.Join(assetsInputDir, "config-v4.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
			},
			setupFunction: func() error {
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "config-v4.yaml"), []byte(networkConfigDual), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if cfg.ServingInfo.BindAddress != "0.0.0.0:6443" {
					return fmt.Errorf("incorrect IPv4 BindAddress: %s", cfg.ServingInfo.BindAddress)
				}
				if cfg.ServingInfo.BindNetwork != "tcp4" {
					return fmt.Errorf("incorrect IPv4 BindNetwork: %s", cfg.ServingInfo.BindNetwork)
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
