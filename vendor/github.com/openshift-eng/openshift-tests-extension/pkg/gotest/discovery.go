package gotest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TestMetadata represents metadata extracted from test source code
type TestMetadata struct {
	Name      string
	Tags      []string
	Timeout   time.Duration
	Lifecycle string
}

// DiscoverTests scans directories and discovers all Test* functions
func DiscoverTests(testDirs []string) ([]TestMetadata, error) {
	var allTests []TestMetadata

	for _, dir := range testDirs {
		tests, err := discoverTestsInDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("failed to discover tests in %s: %w", dir, err)
		}
		allTests = append(allTests, tests...)
	}

	return allTests, nil
}

func discoverTestsInDirectory(dir string) ([]TestMetadata, error) {
	var tests []TestMetadata

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip subdirectories
		if info.IsDir() && path != dir {
			return filepath.SkipDir
		}

		// Only process Go files
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fileTests, err := parseTestFile(path)
		if err != nil {
			return fmt.Errorf("error parsing %s: %w", path, err)
		}

		tests = append(tests, fileTests...)
		return nil
	})

	return tests, err
}

func parseTestFile(filename string) ([]TestMetadata, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var tests []TestMetadata

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}

		// Skip TestMain
		if fn.Name.Name == "TestMain" {
			continue
		}

		// Check if it's a test function
		if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
			continue
		}

		// Extract metadata from comments
		metadata := extractMetadataFromComments(fn.Doc, fn.Name.Name)
		tests = append(tests, metadata)
	}

	return tests, nil
}

func extractMetadataFromComments(doc *ast.CommentGroup, testName string) TestMetadata {
	metadata := TestMetadata{
		Name:      testName,
		Tags:      []string{},
		Lifecycle: "Blocking", // Default
	}

	if doc == nil {
		return metadata
	}

	for _, comment := range doc.List {
		text := strings.TrimPrefix(comment.Text, "//")
		text = strings.TrimSpace(text)

		if strings.HasPrefix(text, "Tags:") {
			tagStr := strings.TrimPrefix(text, "Tags:")
			tagStr = strings.TrimSpace(tagStr)
			tags := strings.Split(tagStr, ",")
			for _, tag := range tags {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					metadata.Tags = append(metadata.Tags, tag)
				}
			}
		}

		if strings.HasPrefix(text, "Timeout:") {
			timeoutStr := strings.TrimSpace(strings.TrimPrefix(text, "Timeout:"))
			if timeout, err := time.ParseDuration(timeoutStr); err == nil {
				metadata.Timeout = timeout
			}
		}

		if strings.HasPrefix(text, "Lifecycle:") {
			metadata.Lifecycle = strings.TrimSpace(strings.TrimPrefix(text, "Lifecycle:"))
		}
	}

	return metadata
}
