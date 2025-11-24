# Custom Go Test Framework for OTE

**A custom test framework (like Ginkgo) that enables standard Go tests to run through OpenShift Tests Extension.**

## Key Features

- ✅ **Zero test code changes** - Use standard `*testing.T`
- ✅ **Automatic discovery** - Parses source files to find tests
- ✅ **No Ginkgo dependency** - Custom framework specifically for Go tests
- ✅ **Full OTE integration** - Parallel/Serial, Blocking/Informing supported
- ✅ **Metadata from comments** - `// Tags:`, `// Timeout:`, `// Lifecycle:`
- ✅ **Runs from source** - No test compilation required

## Quick Start

### 1. Configure in Your Operator

```go
package main

import (
    "github.com/openshift-eng/openshift-tests-extension/pkg/gotest"
)

func main() {
    config := gotest.Config{
        TestPrefix: "[sig-api-machinery] my-operator",
        TestDirectories: []string{
            "test/e2e",
            "test/e2e-encryption",
        },
    }

    specs, _ := gotest.BuildExtensionTestSpecs(config)
    extension.AddSpecs(specs)
}
```

### 2. Write Standard Go Tests

```go
// test/e2e/my_test.go
package e2e

import "testing"

// Tags: Serial
// Timeout: 60m
func TestMyFeature(t *testing.T) {
    // Standard Go test - no changes needed!
}
```

That's it! The framework automatically discovers and runs your tests through OTE.

## How It Works

```
Source Files → AST Parser → Metadata → OTE Specs → go test → Results
```

1. **Discovery** - Scans test directories, parses Go files using AST
2. **Metadata** - Extracts Tags, Timeout, Lifecycle from comments
3. **Integration** - Builds OTE ExtensionTestSpecs
4. **Execution** - Runs tests via `go test -run TestName`
5. **Results** - Converts to OTE format

## Metadata Tags

```go
// Tags: Serial, Slow
// Timeout: 120m
// Lifecycle: Informing
func TestExample(t *testing.T) { }
```

- **Tags**: `Serial`, `Slow`, or custom tags
- **Timeout**: Duration (e.g., `60m`, `2h`)
- **Lifecycle**: `Blocking` (default) or `Informing`

## Benefits

**vs Compiled Binary Approach:**
- Smaller binaries (~55 MB vs ~180 MB)
- No build steps
- Simpler architecture

**vs Ginkgo:**
- Standard Go syntax
- Better IDE support
- Lower learning curve
- No DSL to learn

## Complete Example

See [cluster-kube-apiserver-operator](https://github.com/openshift/cluster-kube-apiserver-operator) for a full implementation.

## Architecture

- **discovery.go** - AST-based test discovery
- **executor.go** - Test execution via subprocess
- **adapter.go** - OTE integration layer

## License

Apache 2.0
