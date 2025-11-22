package gotest

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

//go:embed compiled_tests/*.test compiled_tests/metadata.json
var compiledTests embed.FS

var testMetadataCache map[string]TestMetadataEntry

// MetadataFile holds all test metadata (must match generate_metadata.go)
type MetadataFile struct {
	TestDirectory string              `json:"testDirectory"`
	Tests         []TestMetadataEntry `json:"tests"`
}

// TestMetadataEntry holds metadata for a single test (must match generate_metadata.go)
type TestMetadataEntry struct {
	Name      string   `json:"name"`
	Timeout   string   `json:"timeout,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Lifecycle string   `json:"lifecycle,omitempty"`
}

// BuildExtensionTestSpecs discovers all Go tests from embedded test binaries
// This follows the OTE adapter pattern (similar to Cypress/Ginkgo)
func BuildExtensionTestSpecs() (et.ExtensionTestSpecs, error) {
	var specs et.ExtensionTestSpecs

	// Load metadata from embedded JSON
	if err := loadMetadata(); err != nil {
		return nil, fmt.Errorf("failed to load test metadata: %w", err)
	}

	// Extract embedded test binaries to temporary directory
	tmpDir, err := os.MkdirTemp("", "kube-apiserver-operator-tests-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Walk through embedded test binaries
	err = fs.WalkDir(compiledTests, "compiled_tests", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		// Skip metadata.json - it's not a binary
		if filepath.Base(path) == "metadata.json" {
			return nil
		}

		// Extract binary to temp directory
		data, err := compiledTests.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		binaryName := filepath.Base(path)
		tmpBinary := filepath.Join(tmpDir, binaryName)

		if err := os.WriteFile(tmpBinary, data, 0755); err != nil {
			return fmt.Errorf("failed to write binary %s: %w", tmpBinary, err)
		}

		// Discover tests from this binary
		binarySpecs, err := discoverTestsFromBinary(tmpBinary, binaryName)
		if err != nil {
			return fmt.Errorf("failed to discover tests from %s: %w", binaryName, err)
		}

		specs = append(specs, binarySpecs...)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk embedded tests: %w", err)
	}

	return specs, nil
}

// loadMetadata loads test metadata from embedded JSON file
func loadMetadata() error {
	// Read metadata.json from embedded filesystem
	data, err := compiledTests.ReadFile("compiled_tests/metadata.json")
	if err != nil {
		return fmt.Errorf("failed to read metadata.json: %w", err)
	}

	// Parse JSON
	var metadataFiles []MetadataFile
	if err := json.Unmarshal(data, &metadataFiles); err != nil {
		return fmt.Errorf("failed to parse metadata.json: %w", err)
	}

	// Build lookup map: testName -> metadata
	testMetadataCache = make(map[string]TestMetadataEntry)
	for _, file := range metadataFiles {
		for _, test := range file.Tests {
			testMetadataCache[test.Name] = test
		}
	}

	return nil
}
