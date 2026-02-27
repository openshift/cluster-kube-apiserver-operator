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
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"

	otecmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	oteextension "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	oteextensiontests "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
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
// This method must be called before adding the registry to the OTE framework.
func prepareOperatorTestsRegistry() (*oteextension.Registry, error) {
	registry := oteextension.NewRegistry()
	extension := oteextension.NewExtension("openshift", "payload", "cluster-kube-apiserver-operator")

	// The following suite runs tests that verify the operator’s behaviour.
	// This suite is executed only on pull requests targeting this repository.
	// Tests tagged with both [Operator] and [Serial] are included in this suite.
	extension.AddSuite(oteextension.Suite{
		Name:        "openshift/cluster-kube-apiserver-operator/operator/serial",
		Parallelism: 1,
		Qualifiers: []string{
			`name.contains("[Operator]") && name.contains("[Serial]")`,
		},
	})

	// Tests tagged with [Operator], [Serial], and [Conformance] are included.
	extension.AddSuite(oteextension.Suite{
		Name:        "openshift/cluster-kube-apiserver-operator/conformance/serial",
		Parents:     []string{"openshift/conformance/serial"},
		Parallelism: 1,
		Qualifiers: []string{
			`name.contains("[Serial]") && name.contains("[Conformance]")`,
		},
	})

	specs, err := oteginkgo.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		return nil, fmt.Errorf("couldn't build extension test specs from ginkgo: %w", err)
	}

	// Apply environment filtering based on skip tags in test names
	// This is a generic approach - any test can use these tags to skip on specific platforms
	applyEnvironmentFilters(specs)

	extension.AddSpecs(specs)
	registry.Register(extension)
	return registry, nil
}

// applyEnvironmentFilters applies environment-based filtering to test specs based on tags.
// This provides a generic mechanism for any test to specify platform requirements by adding
// appropriate tags to the test name.
//
// Supported tags:
//   - [Skipped:HyperShift] - excludes test on External topology (HyperShift clusters)
//   - [Skipped:MicroShift] - excludes test on SingleReplica topology (MicroShift clusters)
//   - [FeatureGate:X]      - includes test only when feature gate X is enabled
func applyEnvironmentFilters(specs oteextensiontests.ExtensionTestSpecs) {
	// Map of skip tags to their corresponding topology exclusion CEL expressions
	skipTagToTopology := map[string]string{
		"[Skipped:HyperShift]": "External",
		"[Skipped:MicroShift]": "SingleReplica",
	}

	// Regex to extract feature gate names from [FeatureGate:X] tags
	featureGateRegex := regexp.MustCompile(`\[FeatureGate:([^\]]+)\]`)

	for _, spec := range specs {
		// Apply topology exclusions
		var exclusions []string
		for tag, topology := range skipTagToTopology {
			if strings.Contains(spec.Name, tag) {
				exclusions = append(exclusions, oteextensiontests.TopologyEquals(topology))
			}
		}
		if len(exclusions) > 0 {
			spec.Exclude(oteextensiontests.Or(exclusions...))
		}

		// Apply feature gate requirements - test only runs if feature gate is enabled
		matches := featureGateRegex.FindAllStringSubmatch(spec.Name, -1)
		for _, match := range matches {
			if len(match) > 1 {
				featureGateName := match[1]
				spec.Include(oteextensiontests.FeatureGateEnabled(featureGateName))
			}
		}
	}
}
