package gotest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// discoverTestsFromBinary uses the compiled test binary's -test.list flag
// to discover all Test* functions. This works WITHOUT source code.
func discoverTestsFromBinary(binaryPath string, binaryName string) (et.ExtensionTestSpecs, error) {
	// Run binary with -test.list to discover all tests
	cmd := exec.Command(binaryPath, "-test.list", ".*")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list tests from %s: %w\nOutput: %s", binaryPath, err, string(output))
	}

	var specs et.ExtensionTestSpecs
	scanner := bufio.NewScanner(bytes.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Only process Test* functions (skip other output)
		if !strings.HasPrefix(line, "Test") {
			continue
		}

		testName := line

		// Look up metadata from cache (loaded from metadata.json)
		metadata, hasMetadata := testMetadataCache[testName]
		if !hasMetadata {
			// Default metadata if not found
			metadata = TestMetadataEntry{
				Name:      testName,
				Tags:      []string{}, // Default: No tags = Parallel
				Lifecycle: "Blocking", // Default: Blocking
			}
		}

		// Build full test name with tags
		fullTestName := buildTestName(testName, metadata)

		// Check if test is parallel or serial
		isSerial := false
		for _, tag := range metadata.Tags {
			if tag == "Serial" {
				isSerial = true
				break
			}
		}

		// Create extension test spec
		spec := &et.ExtensionTestSpec{
			Name:   fullTestName,
			Labels: sets.New[string](),
		}

		// All tests must have Run function (for serial/default execution)
		spec.Run = func(ctx context.Context) *et.ExtensionTestResult {
			return runSingleTest(ctx, binaryPath, testName, metadata)
		}

		// Tests without [Serial] tag can ALSO run in parallel
		if !isSerial {
			spec.RunParallel = func(ctx context.Context) *et.ExtensionTestResult {
				return runSingleTest(ctx, binaryPath, testName, metadata)
			}
		}

		// Set lifecycle (default: Informing)
		if strings.EqualFold(metadata.Lifecycle, "Blocking") {
			// Blocking tests - will block CI on failure
			spec.Lifecycle = et.LifecycleBlocking
		} else {
			// Informing tests - won't block CI
			spec.Lifecycle = et.LifecycleInforming
			spec.Labels.Insert("Lifecycle:informing")
		}

		// Set tags (including timeout if specified)
		if metadata.Timeout != "" {
			if spec.Tags == nil {
				spec.Tags = make(map[string]string)
			}
			spec.Tags["timeout"] = metadata.Timeout
		}

		specs = append(specs, spec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading test list: %w", err)
	}

	return specs, nil
}

// buildTestName creates a full test name with tags (excluding Parallel)
// Example: "[sig-api-machinery] kube-apiserver operator TestName [Serial] [Timeout:60m]"
// Note: Parallel tag is NOT included in the name (only used for suite routing)
func buildTestName(testName string, metadata TestMetadataEntry) string {
	var parts []string

	// Base name
	parts = append(parts, fmt.Sprintf("[sig-api-machinery] kube-apiserver operator %s", testName))

	// Add tags (like Serial, Slow) but EXCLUDE Parallel
	for _, tag := range metadata.Tags {
		if tag != "Parallel" {
			parts = append(parts, fmt.Sprintf("[%s]", tag))
		}
	}

	// Add timeout if specified
	if metadata.Timeout != "" {
		parts = append(parts, fmt.Sprintf("[Timeout:%s]", metadata.Timeout))
	}

	return strings.Join(parts, " ")
}
