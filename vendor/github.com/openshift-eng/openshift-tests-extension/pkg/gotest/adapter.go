package gotest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// Config holds configuration for the Go test framework
type Config struct {
	// TestPrefix is prepended to all test names (e.g., "[sig-api-machinery] kube-apiserver operator")
	TestPrefix string

	// TestDirectories are the directories to scan for tests (e.g., "test/e2e", "test/e2e-encryption")
	TestDirectories []string

	// ModuleRoot is the root directory of the Go module (auto-detected if empty)
	ModuleRoot string
}

// BuildExtensionTestSpecs discovers Go tests and converts them to OTE ExtensionTestSpecs
func BuildExtensionTestSpecs(config Config) (et.ExtensionTestSpecs, error) {
	// Auto-detect module root if not provided
	if config.ModuleRoot == "" {
		moduleRoot, err := findModuleRoot()
		if err != nil {
			return nil, fmt.Errorf("failed to find module root: %w", err)
		}
		config.ModuleRoot = moduleRoot
	}

	// Build absolute paths for test directories
	var absoluteTestDirs []string
	testDirMap := make(map[string]string) // testName -> directory

	for _, dir := range config.TestDirectories {
		absoluteDir := filepath.Join(config.ModuleRoot, dir)
		absoluteTestDirs = append(absoluteTestDirs, absoluteDir)

		// Discover tests in this directory to build mapping
		tests, err := discoverTestsInDirectory(absoluteDir)
		if err != nil {
			// Directory might not exist, skip it
			continue
		}

		for _, test := range tests {
			testDirMap[test.Name] = dir
		}
	}

	// Discover all tests
	tests, err := DiscoverTests(absoluteTestDirs)
	if err != nil {
		return nil, fmt.Errorf("failed to discover tests: %w", err)
	}

	// Convert to ExtensionTestSpecs
	specs := make(et.ExtensionTestSpecs, 0, len(tests))
	for _, test := range tests {
		spec := buildTestSpec(config, test, testDirMap[test.Name])
		specs = append(specs, spec)
	}

	return specs, nil
}

func buildTestSpec(config Config, test TestMetadata, testDir string) *et.ExtensionTestSpec {
	// Build test name with prefix and tags
	testName := config.TestPrefix + " " + test.Name

	// Add tags to name (for suite routing)
	for _, tag := range test.Tags {
		testName += fmt.Sprintf(" [%s]", tag)
	}

	// Add timeout to name if specified
	if test.Timeout > 0 {
		testName += fmt.Sprintf(" [Timeout:%s]", test.Timeout)
	}

	// Determine lifecycle
	lifecycle := et.LifecycleBlocking
	if strings.EqualFold(test.Lifecycle, "Informing") {
		lifecycle = et.LifecycleInforming
	}

	// Determine parallelism (Serial tag means no parallelism)
	isSerial := false
	for _, tag := range test.Tags {
		if tag == "Serial" {
			isSerial = true
			break
		}
	}

	// Capture testDir and testName in closure
	capturedTestDir := filepath.Join(config.ModuleRoot, testDir)
	capturedTestName := test.Name
	capturedTimeout := test.Timeout

	// Build Labels set from tags (for OTE filtering)
	labels := sets.New[string](test.Tags...)

	spec := &et.ExtensionTestSpec{
		Name:      testName,
		Labels:    labels,
		Lifecycle: lifecycle,
		Run: func(ctx context.Context) *et.ExtensionTestResult {
			// Execute test
			result := ExecuteTest(ctx, capturedTestDir, capturedTestName, capturedTimeout)

			// Convert to ExtensionTestResult
			oteResult := et.ResultPassed
			if !result.Passed {
				oteResult = et.ResultFailed
			}

			return &et.ExtensionTestResult{
				Result: oteResult,
				Output: result.Output,
			}
		},
	}

	// Apply timeout tag if specified
	if test.Timeout > 0 {
		if spec.Tags == nil {
			spec.Tags = make(map[string]string)
		}
		spec.Tags["timeout"] = test.Timeout.String()
	}

	// Apply isolation (Serial tests need isolation)
	if isSerial {
		spec.Resources = et.Resources{
			Isolation: et.Isolation{},
		}
	}

	return spec
}

// findModuleRoot walks up from current directory to find go.mod
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod")
		}
		dir = parent
	}
}
