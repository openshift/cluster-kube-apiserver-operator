package adapter

//go:generate go run generate_metadata.go

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// GoTestConfig represents configuration for running a go test file
type GoTestConfig struct {
	TestFile    string   // e.g., "operator_test.go"
	TestPattern string   // e.g., "TestOperator.*" or empty for all tests
	Tags        []string // OTE tags like ["Serial", "Slow"]
	Timeout     string   // e.g., "5m", "1h" - optional timeout for the test
	Lifecycle   g.Labels // OTE lifecycle
}

// RunGoTestFile wraps execution of a standard Go test file (ending in _test.go)
// This allows running existing tests WITHOUT any code changes!
// Similar to: https://github.com/openshift-eng/openshift-tests-extension/blob/main/pkg/cypress/util.go
func RunGoTestFile(description string, config GoTestConfig) bool {
	testName := config.TestFile
	if config.TestPattern != "" {
		testName = fmt.Sprintf("%s:%s", config.TestFile, config.TestPattern)
	}

	// Build test name with tags
	fullTestName := description
	if len(config.Tags) > 0 {
		for _, tag := range config.Tags {
			fullTestName += fmt.Sprintf(" [%s]", tag)
		}
	}

	// Add timeout tag if present
	if config.Timeout != "" {
		fullTestName += fmt.Sprintf(" [Timeout:%s]", config.Timeout)
	}

	// Determine lifecycle
	// User requirement: "all old go standard cases should be considered as serial by default but not informing"
	// This means lifecycle should be nil (blocking), not ote.Informing()
	lifecycle := config.Lifecycle

	g.It(fullTestName, lifecycle, func() {
		g.By(fmt.Sprintf("Running go test on %s", testName))

		// Build go test command
		args := []string{"test", "-v"}

		// Add test pattern if specified
		if config.TestPattern != "" {
			args = append(args, "-run", config.TestPattern)
		}

		// Add the test file (use basename only)
		args = append(args, filepath.Base(config.TestFile))

		// Get test root directory (supports TEST_ROOT_DIR env var)
		testRoot, err := getTestRootDir()
		if err != nil {
			g.Fail(fmt.Sprintf("Failed to get test root directory: %v", err))
			return
		}

		// Navigate to test directory
		testDir := filepath.Join(testRoot, filepath.Dir(config.TestFile))
		if !strings.Contains(config.TestFile, "/") && !strings.Contains(config.TestFile, string(filepath.Separator)) {
			testDir = filepath.Join(testRoot, "test", "e2e")
		}

		g.By(fmt.Sprintf("Executing: go %s (in %s)", strings.Join(args, " "), testDir))

		cmd := exec.Command("go", args...)
		cmd.Dir = testDir

		// Capture output
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		// Inherit environment (including KUBECONFIG)
		cmd.Env = os.Environ()

		// Run the test
		err = cmd.Run()

		// Output the results
		stdoutStr := stdout.String()
		stderrStr := stderr.String()

		if stdoutStr != "" {
			g.By("Test Output:")
			for _, line := range strings.Split(stdoutStr, "\n") {
				if line != "" {
					g.GinkgoWriter.Println(line)
				}
			}
		}

		if stderrStr != "" {
			g.By("Test Errors:")
			for _, line := range strings.Split(stderrStr, "\n") {
				if line != "" {
					g.GinkgoWriter.Println(line)
				}
			}
		}

		// Check result
		if err != nil {
			g.Fail(fmt.Sprintf("go test failed: %v\nStdout:\n%s\nStderr:\n%s", err, stdoutStr, stderrStr))
		} else {
			g.By(fmt.Sprintf("go test passed for %s", testName))
		}
	})

	return true
}

// GoTestSuite represents a collection of go test files to run
type GoTestSuite struct {
	Description string
	TestFiles   []GoTestConfig
}

// RunGoTestSuite runs multiple go test files as a suite
func RunGoTestSuite(suite GoTestSuite) bool {
	g.Describe(suite.Description, func() {
		for _, testConfig := range suite.TestFiles {
			testConfig := testConfig // capture loop variable

			testName := testConfig.TestFile
			if testConfig.TestPattern != "" {
				testName = fmt.Sprintf("%s:%s", testConfig.TestFile, testConfig.TestPattern)
			}

			// Build description with tags
			desc := testName
			if len(testConfig.Tags) > 0 {
				for _, tag := range testConfig.Tags {
					desc += fmt.Sprintf(" [%s]", tag)
				}
			}

			// Determine lifecycle
			lifecycle := testConfig.Lifecycle

			g.It(desc, lifecycle, func() {
				g.By(fmt.Sprintf("Running go test on %s", testName))

				// Build go test command
				args := []string{"test", "-v", "-json"}

				// Add test pattern if specified
				if testConfig.TestPattern != "" {
					args = append(args, "-run", testConfig.TestPattern)
				}

				// Add the test file
				args = append(args, filepath.Base(testConfig.TestFile))

				// Get test root directory (supports TEST_ROOT_DIR env var)
				testRoot, err := getTestRootDir()
				if err != nil {
					g.Fail(fmt.Sprintf("Failed to get test root directory: %v", err))
					return
				}

				// Navigate to test directory
				testDir := filepath.Join(testRoot, filepath.Dir(testConfig.TestFile))
				if !strings.Contains(testConfig.TestFile, "/") && !strings.Contains(testConfig.TestFile, string(filepath.Separator)) {
					testDir = filepath.Join(testRoot, "test", "e2e")
				}

				g.By(fmt.Sprintf("Executing: go %s (in %s)", strings.Join(args, " "), testDir))

				cmd := exec.Command("go", args...)
				cmd.Dir = testDir

				// Capture output
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				// Inherit environment
				cmd.Env = os.Environ()

				// Run the test
				runErr := cmd.Run()

				// Parse JSON output
				output := stdout.String()
				lines := strings.Split(output, "\n")

				passed := 0
				failed := 0
				skipped := 0

				for _, line := range lines {
					if line == "" {
						continue
					}

					var event struct {
						Action  string  `json:"Action"`
						Package string  `json:"Package"`
						Test    string  `json:"Test"`
						Output  string  `json:"Output"`
						Elapsed float64 `json:"Elapsed"`
					}

					if err := json.Unmarshal([]byte(line), &event); err != nil {
						// Not JSON, might be regular output
						continue
					}

					switch event.Action {
					case "pass":
						if event.Test != "" {
							passed++
							g.GinkgoWriter.Printf("PASS: %s (%.2fs)\n", event.Test, event.Elapsed)
						}
					case "fail":
						if event.Test != "" {
							failed++
							g.GinkgoWriter.Printf("FAIL: %s (%.2fs)\n", event.Test, event.Elapsed)
						}
					case "skip":
						if event.Test != "" {
							skipped++
							g.GinkgoWriter.Printf("SKIP: %s\n", event.Test)
						}
					case "output":
						if event.Test != "" && event.Output != "" {
							// Log test output
							g.GinkgoWriter.Print(event.Output)
						}
					}
				}

				// Show stderr if present
				if stderr.Len() > 0 {
					g.By("Test Errors:")
					g.GinkgoWriter.Println(stderr.String())
				}

				// Summary
				g.By(fmt.Sprintf("Results: %d passed, %d failed, %d skipped", passed, failed, skipped))

				// Check result
				if runErr != nil || failed > 0 {
					g.Fail(fmt.Sprintf("go test failed for %s: %d tests failed", testName, failed))
				} else {
					g.By(fmt.Sprintf("All tests passed for %s", testName))
				}
			})
		}
	})

	return true
}

// getTestRootDir returns the root directory for test files
// Similar to Cypress pattern: uses TEST_ROOT_DIR env var or falls back to current directory
func getTestRootDir() (string, error) {
	// Check for TEST_ROOT_DIR environment variable (like Cypress)
	if testRoot := os.Getenv("TEST_ROOT_DIR"); testRoot != "" {
		return testRoot, nil
	}

	// Fall back to current working directory
	return os.Getwd()
}

// TestDiscoveryInfo holds information about a discovered test
type TestDiscoveryInfo struct {
	Name    string
	Timeout string   // Empty if no timeout specified
	Tags    []string // Tags from comment like: // Tags: Slow, Serial
}

// DiscoverGoTests automatically discovers all Test* functions from a _test.go file
// testFile can be relative path like "test/e2e/operator_test.go" or just "operator_test.go"
// Returns a list of test discovery info found in the file
func DiscoverGoTests(testFile string) ([]TestDiscoveryInfo, error) {
	// Get test root directory (supports TEST_ROOT_DIR env var)
	testRoot, err := getTestRootDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get test root directory: %v", err)
	}

	// Build full path - testFile might already include "test/e2e/" prefix
	var testFilePath string
	if filepath.IsAbs(testFile) {
		testFilePath = testFile
	} else if strings.HasPrefix(testFile, "test/") || strings.HasPrefix(testFile, "test"+string(filepath.Separator)) {
		// testFile already includes test/ prefix
		testFilePath = filepath.Join(testRoot, testFile)
	} else {
		// Old behavior - assume test/e2e directory
		testFilePath = filepath.Join(testRoot, "test", "e2e", testFile)
	}

	// Read the source file to find Test* functions
	content, err := os.ReadFile(testFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read test file: %v", err)
	}

	// Parse source to find Test* function names, timeouts, and tags
	var tests []TestDiscoveryInfo
	lines := strings.Split(string(content), "\n")
	var currentTimeout string
	var currentTags []string

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check for timeout comment: // Timeout: 10m
		if strings.HasPrefix(trimmedLine, "//") && strings.Contains(trimmedLine, "Timeout:") {
			parts := strings.Split(trimmedLine, "Timeout:")
			if len(parts) == 2 {
				currentTimeout = strings.TrimSpace(parts[1])
			}
		}

		// Check for tags comment: // Tags: Slow, Serial
		if strings.HasPrefix(trimmedLine, "//") && strings.Contains(trimmedLine, "Tags:") {
			parts := strings.Split(trimmedLine, "Tags:")
			if len(parts) == 2 {
				tagStr := strings.TrimSpace(parts[1])
				// Split by comma and trim each tag
				tagList := strings.Split(tagStr, ",")
				currentTags = []string{}
				for _, tag := range tagList {
					trimmedTag := strings.TrimSpace(tag)
					if trimmedTag != "" {
						currentTags = append(currentTags, trimmedTag)
					}
				}
			}
		}

		// Look for "func TestXxx(t *testing.T)" or "func TestXxx(tt *testing.T)" pattern
		if strings.HasPrefix(trimmedLine, "func Test") && (strings.Contains(trimmedLine, "(t *testing.T)") || strings.Contains(trimmedLine, "(tt *testing.T)")) {
			// Extract function name
			parts := strings.Fields(trimmedLine)
			if len(parts) >= 2 {
				funcName := parts[1]
				// Remove the parameter part
				if idx := strings.Index(funcName, "("); idx != -1 {
					funcName = funcName[:idx]
				}
				tests = append(tests, TestDiscoveryInfo{
					Name:    funcName,
					Timeout: currentTimeout,
					Tags:    currentTags,
				})
				// Reset for next test
				currentTimeout = ""
				currentTags = nil
			}
		}
	}

	return tests, nil
}

// AutoDiscoverGoTestFile creates test configs by auto-discovering tests from a file
// NO HARDCODING - automatically finds all Test* functions!
func AutoDiscoverGoTestFile(testFile string, defaultTags []string, defaultLifecycle g.Labels) ([]GoTestConfig, error) {
	tests, err := DiscoverGoTests(testFile)
	if err != nil {
		return nil, err
	}

	var configs []GoTestConfig
	for _, testInfo := range tests {
		// Use tags from comment if present, otherwise use defaultTags
		tags := defaultTags
		if len(testInfo.Tags) > 0 {
			tags = testInfo.Tags
		}

		configs = append(configs, GoTestConfig{
			TestFile:    testFile,
			TestPattern: testInfo.Name,
			Tags:        tags,
			Timeout:     testInfo.Timeout,
			Lifecycle:   defaultLifecycle,
		})
	}

	return configs, nil
}

// DiscoverAllTestFiles finds all *_test.go files in test/e2e* directories
// Returns file paths relative to test root (e.g., "test/e2e/operator_test.go")
// Uses TEST_ROOT_DIR environment variable if set (like Cypress pattern)
func DiscoverAllTestFiles() ([]string, error) {
	testRoot, err := getTestRootDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get test root directory: %v", err)
	}

	testBaseDir := filepath.Join(testRoot, "test")

	// Find all directories matching test/e2e*
	pattern := filepath.Join(testBaseDir, "e2e*")
	dirs, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob test directories: %v", err)
	}

	var testFiles []string

	// Search for *_test.go files in each directory
	for _, dir := range dirs {
		pattern := filepath.Join(dir, "*_test.go")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, match := range matches {
			// Store relative path from test root
			relPath, err := filepath.Rel(testRoot, match)
			if err != nil {
				continue
			}
			testFiles = append(testFiles, relPath)
		}
	}

	return testFiles, nil
}

// AutoDiscoverAllGoTests discovers ALL *_test.go files and their Test* functions
// ZERO HARDCODING - fully automatic discovery!
func AutoDiscoverAllGoTests(defaultTags []string, defaultLifecycle g.Labels) ([]GoTestConfig, error) {
	// Find all *_test.go files
	testFiles, err := DiscoverAllTestFiles()
	if err != nil {
		return nil, err
	}

	var allConfigs []GoTestConfig

	// For each test file, discover its Test* functions
	for _, testFile := range testFiles {
		// Determine tags based on filename
		// Files with "parallel" in name get empty tags (no Serial)
		// Other files get default tags (Serial)
		fileTags := defaultTags
		if strings.Contains(strings.ToLower(testFile), "parallel") {
			fileTags = []string{} // No Serial tag for parallel test files
		}

		configs, err := AutoDiscoverGoTestFile(testFile, fileTags, defaultLifecycle)
		if err != nil {
			// Log but don't fail - some files might not have tests
			fmt.Fprintf(os.Stderr, "Warning: failed to discover tests in %s: %v\n", testFile, err)
			continue
		}
		allConfigs = append(allConfigs, configs...)
	}

	return allConfigs, nil
}

// BuildExtensionTestSpecsFromGoTestMetadata converts GoTestConfig metadata to ExtensionTestSpec
// Similar to: https://github.com/openshift-eng/openshift-tests-extension/blob/main/pkg/cypress/util.go#L45
func BuildExtensionTestSpecsFromGoTestMetadata(metadata []GoTestConfig) ext.ExtensionTestSpecs {
	specs := ext.ExtensionTestSpecs{}

	for _, config := range metadata {
		config := config // capture loop variable

		testName := config.TestFile
		if config.TestPattern != "" {
			testName = fmt.Sprintf("%s:%s", config.TestFile, config.TestPattern)
		}

		// Build test name with tags
		fullTestName := fmt.Sprintf("[sig-api-machinery] kube-apiserver operator Standard Go Tests %s", testName)
		for _, tag := range config.Tags {
			fullTestName += fmt.Sprintf(" [%s]", tag)
		}

		// Add timeout tag if present
		if config.Timeout != "" {
			fullTestName += fmt.Sprintf(" [Timeout:%s]", config.Timeout)
		}

		// Determine lifecycle (config.Lifecycle is g.Labels which is unused here)
		// For standard Go tests, lifecycle is always blocking unless explicitly set
		lifecycle := ext.LifecycleBlocking

		// Create labels set and tags map
		labels := sets.Set[string]{}
		for _, tag := range config.Tags {
			labels[tag] = struct{}{}
		}

		// Create tags map for timeout
		tags := make(map[string]string)
		if config.Timeout != "" {
			tags["timeout"] = config.Timeout
		}

		// Create the test execution function
		runFunc := func(ctx context.Context) *ext.ExtensionTestResult {
			result := &ext.ExtensionTestResult{
				Result: ext.ResultPassed,
			}

			// Build go test command
			args := []string{"test", "-v", "-json"}

			// Add test pattern if specified
			if config.TestPattern != "" {
				args = append(args, "-run", config.TestPattern)
			}

			// Add the test file (use basename only)
			args = append(args, filepath.Base(config.TestFile))

			// Get test root directory
			testRoot, err := getTestRootDir()
			if err != nil {
				result.Result = ext.ResultFailed
				result.Error = fmt.Sprintf("Failed to get test root directory: %v", err)
				return result
			}

			// Navigate to test directory
			testDir := filepath.Join(testRoot, filepath.Dir(config.TestFile))
			if !strings.Contains(config.TestFile, "/") && !strings.Contains(config.TestFile, string(filepath.Separator)) {
				testDir = filepath.Join(testRoot, "test", "e2e")
			}

			cmd := exec.Command("go", args...)
			cmd.Dir = testDir

			// Capture output
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			// Inherit environment
			cmd.Env = os.Environ()

			// Run the test
			runErr := cmd.Run()

			// Parse JSON output and format it for human readability
			output := stdout.String()
			lines := strings.Split(output, "\n")
			passed := 0
			failed := 0
			skipped := 0
			var formattedOutput strings.Builder

			// Build human-readable output from JSON events
			for _, line := range lines {
				if line == "" {
					continue
				}

				var event struct {
					Action  string  `json:"Action"`
					Package string  `json:"Package"`
					Test    string  `json:"Test"`
					Output  string  `json:"Output"`
					Elapsed float64 `json:"Elapsed"`
				}

				if err := json.Unmarshal([]byte(line), &event); err != nil {
					// Not JSON, might be regular output - include it
					continue
				}

				switch event.Action {
				case "run":
					if event.Test != "" {
						formattedOutput.WriteString(fmt.Sprintf("=== RUN   %s\n", event.Test))
					}
				case "output":
					if event.Test != "" && event.Output != "" {
						// Include test output (this captures the actual test logs)
						formattedOutput.WriteString(event.Output)
					}
				case "pass":
					if event.Test != "" {
						passed++
						formattedOutput.WriteString(fmt.Sprintf("--- PASS: %s (%.2fs)\n", event.Test, event.Elapsed))
					}
				case "fail":
					if event.Test != "" {
						failed++
						formattedOutput.WriteString(fmt.Sprintf("--- FAIL: %s (%.2fs)\n", event.Test, event.Elapsed))
					}
				case "skip":
					if event.Test != "" {
						skipped++
						formattedOutput.WriteString(fmt.Sprintf("--- SKIP: %s\n", event.Test))
					}
				}
			}

			// Add summary
			formattedOutput.WriteString(fmt.Sprintf("\nResults: %d passed, %d failed, %d skipped\n", passed, failed, skipped))

			// Add stderr if present
			if stderr.Len() > 0 {
				formattedOutput.WriteString("\nTest Errors:\n")
				formattedOutput.WriteString(stderr.String())
			}

			// Store formatted output instead of raw JSON
			result.Output = formattedOutput.String()

			// Check result
			if runErr != nil || failed > 0 {
				result.Result = ext.ResultFailed
				result.Error = fmt.Sprintf("go test failed for %s: %d tests failed", testName, failed)
				if stderr.Len() > 0 {
					result.Error += "\nStderr: " + stderr.String()
				}
			}

			return result
		}

		// Create ExtensionTestSpec
		spec := &ext.ExtensionTestSpec{
			Name:      fullTestName,
			Labels:    labels,
			Lifecycle: lifecycle,
			Tags:      tags,
			Run:       runFunc,
		}

		specs = append(specs, spec)
	}

	return specs
}
