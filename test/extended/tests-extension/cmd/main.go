/*
This command is used to run the Cluster OpenShift API Server Operator tests extension for OpenShift.
It registers the Cluster OpenShift API Server Operator tests with the OpenShift Tests Extension framework
and provides a command-line interface to execute them.
For further information, please refer to the documentation at:
https://github.com/openshift-eng/openshift-tests-extension/blob/main/cmd/example-tests/main.go
*/
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/spf13/cobra"

	// The import below is necessary to ensure that the OAS operator tests are registered with the extension.
	_ "github.com/openshift/cluster-kube-apiserver-operator/test/extended/tests-extension"
)

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "cluster-kube-apiserver-operator")

	// Suite: conformance/parallel (fast, parallel-safe)
	// Rule 1: Tests without [Serial], [Slow], or extended [Timeout:] tags run in parallel
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!(name.contains("[Serial]") || name.contains("[Slow]") || name.contains("[Timeout:60m]") || name.contains("[Timeout:90m]") || name.contains("[Timeout:120m]"))`,
		},
	})

	// Suite: conformance/serial (explicitly serial tests, but NOT slow or extended timeout tests)
	// Rule 2 & 4: Tests with [Serial] or [Serial][Disruptive] run only in serial suite
	// Exclude [Slow] and extended [Timeout:] tests - they go to slow suite instead
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`name.contains("[Serial]") && !name.contains("[Slow]") && !(name.contains("[Timeout:60m]") || name.contains("[Timeout:90m]") || name.contains("[Timeout:120m]"))`,
		},
	})

	// Suite: optional/slow (long-running and extended timeout tests)
	// Rule 3 & 5: Tests with [Slow] or extended [Timeout:] run in slow suite
	// Tests with [Slow][Disruptive][Timeout:] will run serially due to [Serial] tag
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-kube-apiserver-operator/optional/slow",
		Parents: []string{"openshift/optional/slow"},
		Qualifiers: []string{
			`name.contains("[Slow]") || (name.contains("[Timeout:") && (name.contains("[Timeout:60m]") || name.contains("[Timeout:90m]") || name.contains("[Timeout:120m]")))`,
		},
	})

	// Suite: all (includes everything)
	ext.AddSuite(e.Suite{
		Name: "openshift/cluster-kube-apiserver-operator/all",
	})

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
	ext.IgnoreObsoleteTests(
	// "[sig-openshift-apiserver] <test name here>",
	)

	// Initialize environment before running any tests
	specs.AddBeforeAll(func() {
		// do stuff
	})

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "Cluster OpenShift API Server Operator Tests Extension",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
