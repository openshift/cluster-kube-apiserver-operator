/*
This command is used to run the Cluster Kube API Server Operator tests extension for OpenShift.
It registers the Cluster Kube API Server Operator tests with the OpenShift Tests Extension framework
and provides a command-line interface to execute them.
For further information, please refer to the documentation at:
https://github.com/openshift-eng/openshift-tests-extension/blob/main/cmd/example-tests/main.go
*/
package main

import (
	"fmt"
	"os"
	"strings"

	otecmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	oteextension "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	gotest "github.com/openshift-eng/openshift-tests-extension/pkg/gotest"
	"github.com/spf13/cobra"
	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
)

func main() {
	cmd, err := newOperatorTestCommand()
	if err != nil {
		klog.Fatal(err)
	}

	code := cli.Run(cmd)
	os.Exit(code)
}

func newOperatorTestCommand() (*cobra.Command, error) {
	registry, err := prepareOperatorTestsRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to prepare test registry: %w", err)
	}

	cmd := &cobra.Command{
		Use:   "cluster-kube-apiserver-operator-tests",
		Short: "A binary used to run cluster-kube-apiserver-operator tests as part of OTE.",
		Run: func(cmd *cobra.Command, args []string) {
			// no-op, logic is provided by the OTE framework
			if err := cmd.Help(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(otecmd.DefaultExtensionCommands(registry)...)

	return cmd, nil
}

// prepareOperatorTestsRegistry creates the OTE registry for this operator.
//
// Note:
//
// This method must be called before adding the registry to the OTE framework.
func prepareOperatorTestsRegistry() (*oteextension.Registry, error) {
	registry := oteextension.NewRegistry()
	extension := oteextension.NewExtension("openshift", "payload", "cluster-kube-apiserver-operator")

	// Suite: conformance/parallel (fast, parallel-safe)
	// Rule: Tests without [Serial] or [Slow] tags run in parallel
	// [Timeout:] tag is allowed - it just means the test needs more time
	extension.AddSuite(oteextension.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!(name.contains("[Serial]") || name.contains("[Slow]"))`,
		},
	})

	// Suite: conformance/serial (explicitly serial tests, but NOT slow tests)
	// Rule: Tests with [Serial] but NOT [Slow]
	// [Serial][Timeout:] tests go here (serial test that needs extra time)
	extension.AddSuite(oteextension.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`name.contains("[Serial]") && !name.contains("[Slow]")`,
		},
	})

	// Suite: optional/slow (long-running tests marked with [Slow] tag)
	// Rule: Only tests with [Slow] tag
	// Can be [Slow], [Slow][Serial], [Slow][Timeout:60m], etc.
	extension.AddSuite(oteextension.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/optional/slow",
		Parents: []string{"openshift/optional/slow"},
		Qualifiers: []string{
			`name.contains("[Slow]")`,
		},
	})

	// Suite: all (includes everything)
	extension.AddSuite(oteextension.Suite{
		Name: "openshift/cluster-kube-apiserver-operator/all",
	})

	// Build Go test specs using custom framework (auto-discover all test/e2e* directories)
	goTestConfig := gotest.Config{
		TestPrefix:      "[sig-api-machinery] kube-apiserver operator",
		TestDirectories: discoverTestDirectories(),
	}
	specs, err := gotest.BuildExtensionTestSpecs(goTestConfig)
	if err != nil {
		return nil, fmt.Errorf("couldn't build extension test specs from go tests: %w", err)
	}

	// Define tests to skip (both Ginkgo and GoTest)
	testsToSkip := map[string]bool{
		// Add more tests to skip here
		// "[sig-api-machinery] kube-apiserver operator TestOtherTest [Serial]": true,
	}

	// Filter out skipped tests
	var filteredSpecs et.ExtensionTestSpecs
	for _, spec := range specs {
		if !testsToSkip[spec.Name] {
			filteredSpecs = append(filteredSpecs, spec)
		}
	}
	specs = filteredSpecs

	// Extract timeout from test name if present (e.g., [Timeout:50m])
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		// Look for [Timeout:XXm] or [Timeout:XXh] pattern in test name
		if strings.Contains(spec.Name, "[Timeout:") {
			start := strings.Index(spec.Name, "[Timeout:")
			if start != -1 {
				end := strings.Index(spec.Name[start:], "]")
				if end != -1 {
					// Extract the timeout value (e.g., "50m" from "[Timeout:50m]")
					timeoutTag := spec.Name[start+len("[Timeout:") : start+end]
					if spec.Tags == nil {
						spec.Tags = make(map[string]string)
					}
					spec.Tags["timeout"] = timeoutTag
				}
			}
		}
	})

	// Ignore obsolete tests (for update command validation only)
	extension.IgnoreObsoleteTests(
	//"[sig-api-machinery] kube-apiserver operator TestEncryptionRotation [Serial]",
	)

	// Add the discovered test specs to the extension
	extension.AddSpecs(specs)

	registry.Register(extension)
	return registry, nil
}

// discoverTestDirectories automatically finds all test/e2e* directories
func discoverTestDirectories() []string {
	var dirs []string

	// Find all test/e2e* directories
	entries, err := os.ReadDir("test")
	if err != nil {
		klog.Warningf("Failed to read test directory: %v", err)
		return dirs
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "e2e") {
			dirs = append(dirs, fmt.Sprintf("test/%s", entry.Name()))
		}
	}

	return dirs
}
