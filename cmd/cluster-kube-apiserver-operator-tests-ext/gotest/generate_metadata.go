//go:build ignore
// +build ignore

package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// TestMetadataEntry holds metadata for a single test
type TestMetadataEntry struct {
	Name      string   `json:"name"`
	Timeout   string   `json:"timeout,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Lifecycle string   `json:"lifecycle,omitempty"`
}

// MetadataFile holds all test metadata for a test directory
type MetadataFile struct {
	TestDirectory string              `json:"testDirectory"`
	Tests         []TestMetadataEntry `json:"tests"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <output-file> <test-dir1> <test-dir2> ...\n", os.Args[0])
		os.Exit(1)
	}

	outputFile := os.Args[1]
	testDirs := os.Args[2:]

	var allMetadata []MetadataFile

	for _, testDir := range testDirs {
		metadata, err := discoverTestMetadata(testDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to discover metadata in %s: %v\n", testDir, err)
			continue
		}
		allMetadata = append(allMetadata, metadata)
	}

	// Write metadata to JSON file
	data, err := json.MarshalIndent(allMetadata, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal metadata: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write metadata file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated metadata for %d test directories\n", len(allMetadata))
}

func discoverTestMetadata(testDir string) (MetadataFile, error) {
	metadata := MetadataFile{
		TestDirectory: testDir,
		Tests:         []TestMetadataEntry{},
	}

	// Find all *_test.go files
	testFiles, err := filepath.Glob(filepath.Join(testDir, "*_test.go"))
	if err != nil {
		return metadata, err
	}

	for _, testFile := range testFiles {
		tests, err := parseTestFile(testFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", testFile, err)
			continue
		}
		metadata.Tests = append(metadata.Tests, tests...)
	}

	return metadata, nil
}

func parseTestFile(testFile string) ([]TestMetadataEntry, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, testFile, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var tests []TestMetadataEntry

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Check if this is a Test* function
		if !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}

		// Verify it has the right signature: func TestXxx(t *testing.T)
		if fn.Type.Params.NumFields() != 1 {
			continue
		}

		test := TestMetadataEntry{
			Name:      fn.Name.Name,
			Tags:      []string{}, // Default: No tags = Parallel (add "// Tags: Serial" to make serial)
			Lifecycle: "Blocking", // Default: Blocking (add "// Lifecycle: Informing" to override)
		}

		// Parse comments
		if fn.Doc != nil {
			for _, comment := range fn.Doc.List {
				text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))

				// Parse Timeout: 60m
				if strings.HasPrefix(text, "Timeout:") {
					test.Timeout = strings.TrimSpace(strings.TrimPrefix(text, "Timeout:"))
				}

				// Parse Tags: Serial, Slow
				if strings.HasPrefix(text, "Tags:") {
					tagStr := strings.TrimSpace(strings.TrimPrefix(text, "Tags:"))
					tags := strings.Split(tagStr, ",")
					test.Tags = []string{}
					for _, tag := range tags {
						test.Tags = append(test.Tags, strings.TrimSpace(tag))
					}
				}

				// Parse Lifecycle: Blocking or Informing
				if strings.HasPrefix(text, "Lifecycle:") {
					test.Lifecycle = strings.TrimSpace(strings.TrimPrefix(text, "Lifecycle:"))
				}
			}
		}

		tests = append(tests, test)
	}

	return tests, nil
}
