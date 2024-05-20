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

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	libgoaudit "github.com/openshift/library-go/pkg/operator/apiserver/audit"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	genericrenderoptions "github.com/openshift/library-go/pkg/operator/render/options"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
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

	infrastructureHA = `
apiVersion: config.openshift.io/v1
kind: Infrastructure
metadata:
  creationTimestamp: null
  name: cluster
spec: {}
status:
  controlPlaneTopology: HighlyAvailable
`

	infrastructureSNO = `
apiVersion: config.openshift.io/v1
kind: Infrastructure
metadata:
  creationTimestamp: null
  name: cluster
spec: {}
status:
  controlPlaneTopology: SingleReplica
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

	defaultFGDir := filepath.Join("testdata", "rendered", "default-fg")

	tests := []struct {
		// note the name is used as a name for a temporary directory
		name            string
		args            []string
		overrides       []func(*renderOpts)
		setupFunction   func() error
		testFunction    func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error
		podTestFunction func(cfg *corev1.Pod) error
	}{
		{
			name: "checks feature gates",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			overrides: []func(*renderOpts){
				func(opts *renderOpts) {
					opts.groupVersionsByFeatureGate = map[configv1.FeatureGateName][]schema.GroupVersion{
						"Foo": {{Group: "foos.example.com", Version: "v4alpha7"}},
					}
				},
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				actualGates, ok := cfg.APIServerArguments["feature-gates"]
				if !ok {
					return fmt.Errorf("missing \"feature-gates\" entry in APIServerArguments")
				}
				expectedGates := []string{"Bar=false", "Foo=true", "OpenShiftPodSecurityAdmission=true"}
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

				actualRuntimeConfig, ok := cfg.APIServerArguments["runtime-config"]
				if !ok {
					return fmt.Errorf(`missing expected "runtime-config" entry in APIServerArguments`)
				}
				expectedRuntimeConfig := []string{"foos.example.com/v4alpha7=true"}
				if len(expectedRuntimeConfig) != len(actualRuntimeConfig) {
					return fmt.Errorf("expected runtime-config of len %d, got: %v (len %d)", len(expectedRuntimeConfig), actualRuntimeConfig, len(actualRuntimeConfig))
				}
				for i := 0; i < len(expectedRuntimeConfig); i++ {
					if expectedRuntimeConfig[i] != actualRuntimeConfig[i] {
						return fmt.Errorf("expected %dth runtime-config entry %q, got %q", i+1, expectedRuntimeConfig[i], actualRuntimeConfig[i])
					}
				}

				return nil
			},
		},
		{
			name: "checks BindAddress under IPv6",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-config-file=" + filepath.Join(assetsInputDir, "config-v6.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
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
			name: "checks BindAddress and ServicesSubnet under dual IPv4-IPv6",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-config-file=" + filepath.Join(assetsInputDir, "config-dual.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
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
			name: "checks service account issuer when authentication no exists",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				issuer := cfg.APIServerArguments["service-account-issuer"]
				expectedIssuer := kubecontrolplanev1.Arguments{"https://kubernetes.default.svc"}
				if !reflect.DeepEqual(issuer, expectedIssuer) {
					return fmt.Errorf("expected the service-account-issuer to be %q, but it was %q", expectedIssuer, issuer)
				}
				return nil
			},
		},
		{
			name: "checks service account issuer when authentication exists but empty",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			setupFunction: func() error {
				data := ``
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "authentication.yaml"), []byte(data), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				issuer := cfg.APIServerArguments["service-account-issuer"]
				expectedIssuer := kubecontrolplanev1.Arguments{"https://kubernetes.default.svc"}
				if !reflect.DeepEqual(issuer, expectedIssuer) {
					return fmt.Errorf("expected the service-account-issuer to be %q, but it was %q", expectedIssuer, issuer)
				}
				return nil
			},
		},
		{
			name: "checks service account issuer when authentication exists but empty spec",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
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
				issuer := cfg.APIServerArguments["service-account-issuer"]
				expectedIssuer := kubecontrolplanev1.Arguments{"https://kubernetes.default.svc"}
				if !reflect.DeepEqual(issuer, expectedIssuer) {
					return fmt.Errorf("expected the service-account-issuer to be %q, but it was %q", expectedIssuer, issuer)
				}
				return nil
			},
		},
		{
			name: "checks service account issuer when authentication spec has issuer set",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--cluster-auth-file=" + filepath.Join(assetsInputDir, "authentication.yaml"),
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
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
		{
			name: "no user provided bound-sa-signing-keys -> generate the keys",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
		},
		{
			name: "user provided bound-sa-signing-key and public part",
			args: []string{
				"--asset-input-dir=" + filepath.Join(assetsInputDir, "2"),
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			setupFunction: func() error {
				data := `DUMMY DATA`
				if err := os.Mkdir(filepath.Join(assetsInputDir, "2"), 0700); err != nil {
					return err
				}
				if err := ioutil.WriteFile(filepath.Join(assetsInputDir, "2", "bound-service-account-signing-key.key"), []byte(data), 0644); err != nil {
					return err
				}
				if err := ioutil.WriteFile(filepath.Join(assetsInputDir, "2", "bound-service-account-signing-key.pub"), []byte(data), 0644); err != nil {
					return err
				}
				return nil
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["service-account-signing-key-file"]) == 0 {
					return fmt.Errorf("expected the service-account-issuer to be set, but it was empty")
				}
				if !reflect.DeepEqual(cfg.APIServerArguments["service-account-signing-key-file"], kubecontrolplanev1.Arguments([]string{"/etc/kubernetes/secrets/bound-service-account-signing-key.key"})) {
					return fmt.Errorf("expected the service-account-issuer to be [ /etc/kubernetes/secrets/bound-service-account-signing-key.key ], but it was %s", cfg.APIServerArguments["service-account-signing-key-file"])
				}
				if !reflect.DeepEqual(
					cfg.APIServerArguments["service-account-key-file"],
					kubecontrolplanev1.Arguments([]string{"/etc/kubernetes/secrets/service-account.pub", "/etc/kubernetes/secrets/bound-service-account-signing-key.pub"}),
				) {
					return fmt.Errorf("expected the service-account-issuer to be [ /etc/kubernetes/secrets/service-account.pub , /etc/kubernetes/secrets/bound-service-account-signing-key.pub ], but it was %s", cfg.APIServerArguments["service-account-key-file"])
				}
				return nil
			},
		},
		{
			name: "no infrastructure file",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["shutdown-delay-duration"]) == 0 {
					return fmt.Errorf("expected a shutdown-delay-duration argument")
				}
				if got, expected := cfg.APIServerArguments["shutdown-delay-duration"][0], "70s"; got != expected {
					return fmt.Errorf("expected shutdown-delay-duration=%q, but found %s", expected, got)
				}
				return nil
			},
			podTestFunction: func(pod *corev1.Pod) error {
				if pod.Spec.TerminationGracePeriodSeconds == nil {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				if got, expected := *pod.Spec.TerminationGracePeriodSeconds, int64(135); got != expected {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				return nil
			},
		},
		{
			name: "infrastructure file with HA topology",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--infra-config-file=" + filepath.Join(assetsInputDir, "infrastructure.yaml"),
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			setupFunction: func() error {
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "infrastructure.yaml"), []byte(infrastructureHA), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["shutdown-delay-duration"]) == 0 {
					return fmt.Errorf("expected a shutdown-delay-duration argument")
				}
				if got, expected := cfg.APIServerArguments["shutdown-delay-duration"][0], "70s"; got != expected {
					return fmt.Errorf("expected shutdown-delay-duration=%q, but found %s", expected, got)
				}
				return nil
			},
			podTestFunction: func(pod *corev1.Pod) error {
				if pod.Spec.TerminationGracePeriodSeconds == nil {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				if got, expected := *pod.Spec.TerminationGracePeriodSeconds, int64(135); got != expected {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				return nil
			},
		},
		{
			name: "infrastructure file with SNO topology",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--infra-config-file=" + filepath.Join(assetsInputDir, "infrastructure.yaml"),
				"--payload-version=test",
				"--rendered-manifest-files=" + defaultFGDir,
			},
			setupFunction: func() error {
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "infrastructure.yaml"), []byte(infrastructureSNO), 0644)
			},
			testFunction: func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error {
				if len(cfg.APIServerArguments["shutdown-delay-duration"]) == 0 {
					return fmt.Errorf("expected a shutdown-delay-duration argument")
				}
				if got, expected := cfg.APIServerArguments["shutdown-delay-duration"][0], "0s"; got != expected {
					return fmt.Errorf("expected shutdown-delay-duration=%q, but found %s", expected, got)
				}
				return nil
			},
			podTestFunction: func(pod *corev1.Pod) error {
				if pod.Spec.TerminationGracePeriodSeconds == nil {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				if got, expected := *pod.Spec.TerminationGracePeriodSeconds, int64(15); got != expected {
					return fmt.Errorf("expected a spec.terminationGracePeriodSeconds to be set in pod manifest")
				}
				return nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			err = runRender(test.args, test.overrides)
			if err != nil {
				t.Fatalf("%s: got unexpected error %v", test.name, err)
			}

			rawConfigFile, err := ioutil.ReadFile(filepath.Join(outputDir, "configs", "config.yaml"))
			if err != nil {
				t.Fatalf("cannot read the rendered config file, error: %v", err)
			}
			cfg := &kubecontrolplanev1.KubeAPIServerConfig{}
			if err := kyaml.Unmarshal(rawConfigFile, cfg); err != nil {
				t.Fatalf("cannot unmarshal config into KubeAPIServerConfig, error: %v", err)
			}

			rawStaticPodFile, err := ioutil.ReadFile(filepath.Join(outputDir, "manifests", "bootstrap-manifests", "kube-apiserver-pod.yaml"))
			if err != nil {
				t.Fatalf("cannot read the rendered config file, error: %v", err)
			}
			pod := corev1.Pod{}
			if err := kyaml.Unmarshal(rawStaticPodFile, &pod); err != nil {
				t.Fatalf("cannot unmarshal config into Pod, error: %v", err)
			}

			if test.testFunction != nil {
				if err := test.testFunction(cfg); err != nil {
					t.Fatalf("%q reports incorrect config file, error: %v\n\n%s", test.name, err, string(rawConfigFile))
				}
			}
			if test.podTestFunction != nil {
				if err := test.podTestFunction(&pod); err != nil {
					t.Fatalf("%q reports incorrect static pod file, error: %v\n\n%s", test.name, err, string(rawStaticPodFile))
				}
			}
		})
	}
}

func TestGetDefaultConfigWithAuditPolicy(t *testing.T) {
	raw, err := bootstrapDefaultConfig(featuregates.NewFeatureGate([]configv1.FeatureGateName{features.FeatureGateOpenShiftPodSecurityAdmission}, nil))
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

	defaultPolicy, err := libgoaudit.DefaultPolicy()
	require.NoError(t, err)
	rawPolicyJSON, err := kyaml.ToJSON(defaultPolicy)
	require.NoError(t, err)
	policyExpected, err := convertToUnstructured(rawPolicyJSON)
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

func runRender(args []string, overrides []func(*renderOpts)) error {
	defaultTestOverrides := []func(*renderOpts){
		func(opts *renderOpts) {
			opts.groupVersionsByFeatureGate = map[configv1.FeatureGateName][]schema.GroupVersion{}
		},
	}
	c := newRenderCommand(append(defaultTestOverrides, overrides...)...)
	c.SetArgs(args)
	return c.Execute()
}

func Test_renderOpts_Validate(t *testing.T) {
	assetsInputDir, err := ioutil.TempDir("", "testdata")
	if err != nil {
		t.Errorf("unable to create assets input directory, error: %v", err)
	}
	templateDir := filepath.Join("..", "..", "..", "bindata", "bootkube")

	tests := []struct {
		name          string
		assetInputDir string
		setupFunction func() error
		testFunction  func(cfg *kubecontrolplanev1.KubeAPIServerConfig) error
		wantErr       bool
	}{
		{
			name:          "user provided bound-sa-signing-key only no public part",
			assetInputDir: filepath.Join(assetsInputDir, "0"),
			setupFunction: func() error {
				data := `DUMMY DATA`
				if err := os.Mkdir(filepath.Join(assetsInputDir, "0"), 0700); err != nil {
					return err
				}
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "0", "bound-service-account-signing-key.key"), []byte(data), 0600)
			},
			wantErr: true,
		},
		{
			name:          "user provided bound-sa-signing-key only public part",
			assetInputDir: filepath.Join(assetsInputDir, "1"),
			setupFunction: func() error {
				data := `DUMMY DATA`
				if err := os.Mkdir(filepath.Join(assetsInputDir, "1"), 0700); err != nil {
					return err
				}
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "1", "bound-service-account-signing-key.pub"), []byte(data), 0644)
			},
			wantErr: true,
		},
		{
			name:          "user provided bound-sa-signing-key - both keys exist",
			assetInputDir: filepath.Join(assetsInputDir, "2"),
			setupFunction: func() error {
				data := `DUMMY DATA`
				if err := os.Mkdir(filepath.Join(assetsInputDir, "2"), 0700); err != nil {
					return err
				}
				if err := ioutil.WriteFile(filepath.Join(assetsInputDir, "2", "bound-service-account-signing-key.pub"), []byte(data), 0644); err != nil {
					return err
				}
				return ioutil.WriteFile(filepath.Join(assetsInputDir, "2", "bound-service-account-signing-key.key"), []byte(data), 0600)
			},
		},
		{
			name:          "user provided bound-sa-signing-key - neither key exists",
			assetInputDir: filepath.Join(assetsInputDir, "3"),
			setupFunction: func() error {
				return os.Mkdir(filepath.Join(assetsInputDir, "3"), 0700)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outDirName := strings.ReplaceAll(tt.name, " ", "_")
			teardown, outputDir, err := setupAssetOutputDir(outDirName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer teardown()

			if err := tt.setupFunction(); err != nil {
				t.Fatalf("failed to set up, error: %v", err)
			}

			r := &renderOpts{
				generic:  *genericrenderoptions.NewGenericOptions(),
				manifest: *genericrenderoptions.NewManifestOptions("kube-apiserver", "openshift/origin-hyperkube:latest"),

				lockHostPath:   "/var/run/kubernetes/lock",
				etcdServerURLs: []string{"https://127.0.0.1:2379"},
				etcdServingCA:  "root-ca.crt",
			}
			r.generic.TemplatesDir = templateDir

			r.generic.AssetInputDir = tt.assetInputDir
			r.generic.AssetOutputDir = filepath.Join(outputDir, "manifests")
			r.generic.ConfigOutputFile = filepath.Join(outputDir, "configs", "config.yaml")

			if err := r.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("renderOpts.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
