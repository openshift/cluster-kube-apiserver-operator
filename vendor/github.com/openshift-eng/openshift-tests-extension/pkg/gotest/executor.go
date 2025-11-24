package gotest

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// TestResult represents the result of running a test
type TestResult struct {
	TestName string
	Passed   bool
	Output   string
	Duration time.Duration
	Error    error
}

// ExecuteTest runs a single test via go test subprocess
func ExecuteTest(ctx context.Context, testDir string, testName string, timeout time.Duration) *TestResult {
	start := time.Now()

	// Apply timeout if specified
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Build go test command
	cmd := exec.CommandContext(ctx, "go", "test", "-v", "-run", fmt.Sprintf("^%s$", testName), testDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute test
	err := cmd.Run()
	duration := time.Since(start)

	// Combine output
	output := stdout.String() + "\n" + stderr.String()

	result := &TestResult{
		TestName: testName,
		Passed:   err == nil,
		Output:   output,
		Duration: duration,
		Error:    err,
	}

	return result
}
