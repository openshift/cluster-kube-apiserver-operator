package adapter

//go:generate go run generate_metadata.go

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	g "github.com/onsi/ginkgo/v2"
	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// Embed precompiled test binaries (compiled during build)
//
//go:embed compiled_tests/*.test
var embeddedTestBinaries embed.FS

var (
	extractedBinariesPath string
	extractOnce           sync.Once
)

// GoTestConfig represents configuration for running a go test file
type GoTestConfig struct {
	TestFile    string   // e.g., "operator_test.go"
	TestPattern string   // e.g., "TestOperator.*" or empty for all tests
	Tags        []string // OTE tags like ["Serial", "Slow"]
	Timeout     string   // e.g., "5m", "1h" - optional timeout for the test
	Lifecycle   string   // "Blocking" or "Informing" - defaults to "Informing"
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
	Name      string
	Timeout   string   // Empty if no timeout specified
	Tags      []string // Tags from comment like: // Tags: Slow, Serial
	Lifecycle string   // "Blocking" or "Informing" from comment like: // Lifecycle: Blocking
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

	// Parse source to find Test* function names, timeouts, tags, and lifecycle
	var tests []TestDiscoveryInfo
	lines := strings.Split(string(content), "\n")
	var currentTimeout string
	var currentTags []string
	var currentLifecycle string

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

		// Check for lifecycle comment: // Lifecycle: Blocking or // Lifecycle: Informing
		if strings.HasPrefix(trimmedLine, "//") && strings.Contains(trimmedLine, "Lifecycle:") {
			parts := strings.Split(trimmedLine, "Lifecycle:")
			if len(parts) == 2 {
				currentLifecycle = strings.TrimSpace(parts[1])
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
					Name:      funcName,
					Timeout:   currentTimeout,
					Tags:      currentTags,
					Lifecycle: currentLifecycle,
				})
				// Reset for next test
				currentTimeout = ""
				currentTags = nil
				currentLifecycle = ""
			}
		}
	}

	return tests, nil
}

// AutoDiscoverGoTestFile creates test configs by auto-discovering tests from a file
// NO HARDCODING - automatically finds all Test* functions!
func AutoDiscoverGoTestFile(testFile string, defaultTags []string, defaultLifecycle string) ([]GoTestConfig, error) {
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

		// Use lifecycle from comment if present, otherwise use defaultLifecycle
		lifecycle := defaultLifecycle
		if testInfo.Lifecycle != "" {
			lifecycle = testInfo.Lifecycle
		}

		configs = append(configs, GoTestConfig{
			TestFile:    testFile,
			TestPattern: testInfo.Name,
			Tags:        tags,
			Timeout:     testInfo.Timeout,
			Lifecycle:   lifecycle,
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
// Default lifecycle is "Informing" unless overridden by comment tags
func AutoDiscoverAllGoTests(defaultTags []string, defaultLifecycle string) ([]GoTestConfig, error) {
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

// buildExtensionTestSpecsFromMetadata converts GoTestConfig metadata to ExtensionTestSpec
// Following the Cypress pattern: https://github.com/openshift-eng/openshift-tests-extension/blob/main/pkg/cypress/util.go
// This is an internal helper function. The public API is in standard_go_tests.go
func buildExtensionTestSpecsFromMetadata(metadata []GoTestConfig) ext.ExtensionTestSpecs {
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

		// Determine lifecycle from config (default is Informing)
		lifecycle := ext.LifecycleInforming
		if strings.EqualFold(config.Lifecycle, "Blocking") {
			lifecycle = ext.LifecycleBlocking
		}

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

		// Create the test execution function (following Cypress pattern)
		runFunc := func(ctx context.Context) *ext.ExtensionTestResult {
			return runGoTest(testName, config.TestFile, config.TestPattern)
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

// runGoTest executes a precompiled Go test binary (similar to runCypressTest in Cypress adapter)
// This follows the pattern: https://github.com/openshift-eng/openshift-tests-extension/blob/main/pkg/cypress/util.go
func runGoTest(testName, testFile, testPattern string) *ext.ExtensionTestResult {
	result := &ext.ExtensionTestResult{
		Name: testName,
	}

	// Get the precompiled test binary path
	testBinary := getCompiledTestBinary(testFile)
	if testBinary == "" {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("precompiled test binary not found for %s", testFile)
		return result
	}

	// Build test command arguments
	args := []string{"-test.v"}
	if testPattern != "" {
		args = append(args, "-test.run", testPattern)
	}

	// Execute the precompiled test binary
	cmd := exec.Command(testBinary, args...)

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Inherit environment (including KUBECONFIG)
	cmd.Env = os.Environ()

	// Run the test
	runErr := cmd.Run()

	// Parse output
	output := stdout.String()
	stderrStr := stderr.String()

	// Determine result
	if runErr != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("test failed: %v", runErr)
		if stderrStr != "" {
			result.Error += "\n" + stderrStr
		}
	} else {
		result.Result = ext.ResultPassed
	}

	result.Output = output
	if stderrStr != "" {
		result.Output += "\nStderr:\n" + stderrStr
	}

	return result
}

// extractEmbeddedBinaries extracts all embedded test binaries to a temp directory
// This is called once on first use
func extractEmbeddedBinaries() error {
	var extractErr error
	extractOnce.Do(func() {
		// Create temp directory for extracted binaries
		tempDir, err := os.MkdirTemp("", "gotest-binaries-*")
		if err != nil {
			extractErr = fmt.Errorf("failed to create temp directory: %w", err)
			return
		}
		extractedBinariesPath = tempDir

		// Extract all .test files from embedded FS
		err = fs.WalkDir(embeddedTestBinaries, "compiled_tests", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".test") {
				return nil
			}

			// Read embedded file
			data, err := embeddedTestBinaries.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read embedded file %s: %w", path, err)
			}

			// Write to temp directory
			fileName := filepath.Base(path)
			outputPath := filepath.Join(tempDir, fileName)
			if err := os.WriteFile(outputPath, data, 0755); err != nil {
				return fmt.Errorf("failed to write file %s: %w", outputPath, err)
			}

			return nil
		})
		if err != nil {
			extractErr = fmt.Errorf("failed to extract binaries: %w", err)
			return
		}
	})
	return extractErr
}

// getCompiledTestBinary returns the path to the precompiled test binary for a given test file
// Binary name is derived from directory name (e.g., test/e2e -> e2e.test)
func getCompiledTestBinary(testFile string) string {
	// Extract embedded binaries if not already done
	if err := extractEmbeddedBinaries(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract embedded test binaries: %v\n", err)
		return ""
	}

	// Determine binary name from directory: test/e2e-encryption -> e2e-encryption.test
	testDir := filepath.Dir(testFile)           // "test/e2e-encryption"
	baseName := filepath.Base(testDir)          // "e2e-encryption"
	binaryName := baseName + ".test"            // "e2e-encryption.test"

	// Return path to extracted binary
	binaryPath := filepath.Join(extractedBinariesPath, binaryName)
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath
	}

	return ""
}
