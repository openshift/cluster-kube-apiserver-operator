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
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"
	"k8s.io/component-base/cli"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"

	// The import below is necessary to ensure that the tests are registered with the extension.
	_ "github.com/openshift/cluster-kube-apiserver-operator/test/e2e"
)

func main() {
	registry := prepareOperatorTestsRegistry()
	cmd := &cobra.Command{
		Use:   "cluster-kube-apiserver-operator-tests",
		Short: "A binary used to run cluster-kube-apiserver-operator tests as part of OTE.",
		Run: func(cmd *cobra.Command, args []string) {
			// no-op, logic is provided by the OTE framework
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(otecmd.DefaultExtensionCommands(registry)...)

	code := cli.Run(cmd)
	os.Exit(code)
}

// prepareOperatorTestsRegistry creates the OTE registry for this operator.
//
// Note:
//
// This method must be called before adding the registry to the OTE framework.
func prepareOperatorTestsRegistry() *oteextension.Registry {
	registry := oteextension.NewRegistry()
	extension := oteextension.NewExtension("openshift", "payload", "cluster-kube-apiserver-operator")

	// Suite: conformance/parallel (fast, parallel-safe)
	// Rule: Tests without [Serial], [Slow], or [Timeout:] tags run in parallel
	extension.AddSuite(oteextension.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!(name.contains("[Serial]") || name.contains("[Slow]") || name.contains("[Timeout:"))`,
		},
	})

	// Suite: conformance/serial (explicitly serial tests, but NOT slow tests)
	// Rule 2 & 4: Tests with [Serial] or [Serial][Disruptive] run only in serial suite
	// Tests with [Serial][Timeout:] go to serial (timeout on serial test)
	// Exclude [Slow] tests - they go to slow suite instead
	// Parallelism: 1 enforces serial execution even when run without -c 1 flag
	extension.AddSuite(oteextension.Suite{
		Name:        "openshift/cluster-kube-apiserver-operator/conformance/serial",
		Parents:     []string{"openshift/conformance/serial"},
		Parallelism: 1,
		Qualifiers: []string{
			`name.contains("[Serial]") && !name.contains("[Slow]")`,
		},
	})

	// Suite: optional/slow (long-running tests and non-serial timeout tests)
	// Rule 3 & 5: Tests with [Slow] OR tests with [Timeout:] that are NOT [Serial]
	// Tests with [Slow][Disruptive][Timeout:] will run serially due to [Serial] tag
	// Parallelism: 1 enforces serial execution even when run without -c 1 flag
	extension.AddSuite(oteextension.Suite{
		Name:        "openshift/cluster-kube-apiserver-operator/optional/slow",
		Parents:     []string{"openshift/optional/slow"},
		Parallelism: 1,
		Qualifiers: []string{
			`name.contains("[Slow]") || (name.contains("[Timeout:") && !name.contains("[Serial]"))`,
		},
	})

	// Suite: all (includes everything)
	extension.AddSuite(oteextension.Suite{
		Name: "openshift/cluster-kube-apiserver-operator/all",
	})

	// Build ginkgo test specs from the test/e2e package
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Ensure [Disruptive] tests are also [Serial]
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		if strings.Contains(spec.Name, "[Disruptive]") && !strings.Contains(spec.Name, "[Serial]") {
			spec.Name = strings.ReplaceAll(
				spec.Name,
				"[Disruptive]",
				"[Serial][Disruptive]",
			)
		}
	})

	// Preserve original-name labels for renamed tests
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "original-name:") {
				parts := strings.SplitN(label, "original-name:", 2)
				if len(parts) > 1 {
					spec.OriginalName = parts[1]
				}
			}
		}
	})

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

	// Ignore obsolete tests
	extension.IgnoreObsoleteTests(
	// "[sig-api-machinery] <test name here>",
	)

	// Initialize environment before running any tests
	specs.AddBeforeAll(func() {
		// do stuff
	})

	// Add the discovered test specs to the extension
	extension.AddSpecs(specs)

	registry.Register(extension)
	return registry
}
