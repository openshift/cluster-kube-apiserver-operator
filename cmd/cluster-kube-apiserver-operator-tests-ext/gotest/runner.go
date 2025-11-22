package gotest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"time"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

// runSingleTest executes a single test from the compiled binary
// Uses Go's built-in -test.run flag to run exactly one test
func runSingleTest(ctx context.Context, binaryPath string, testName string, metadata TestMetadataEntry) *et.ExtensionTestResult {
	startTime := time.Now()

	result := &et.ExtensionTestResult{
		Name:      testName,
		Lifecycle: et.LifecycleInforming,
		StartTime: nil, // Will be set by OTE framework
		EndTime:   nil, // Will be set by OTE framework
	}

	if metadata.Lifecycle == "Blocking" {
		result.Lifecycle = et.LifecycleBlocking
	}

	// Use -test.run with exact match (^TestName$)
	// regexp.QuoteMeta ensures special characters in test name are escaped
	testPattern := "^" + regexp.QuoteMeta(testName) + "$"

	cmd := exec.CommandContext(ctx, binaryPath,
		"-test.run", testPattern,
		"-test.v", // Verbose output
	)

	// Capture output AND stream to stdout/stderr for real-time logging
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, os.Stdout)
	cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)

	// Run the test
	err := cmd.Run()

	// Calculate duration
	duration := time.Since(startTime)
	result.Duration = int64(duration.Seconds())

	// Combine stdout and stderr for output
	output := stdout.String() + stderr.String()
	result.Output = output

	// Set result based on error
	if err != nil {
		result.Result = et.ResultFailed
		result.Error = fmt.Sprintf("test %s failed: %v", testName, err)
	} else {
		result.Result = et.ResultPassed
	}

	return result
}
