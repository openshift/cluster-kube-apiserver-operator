# Cluster Kube API Server Operator Tests Extension
========================

This repository contains the tests for the Cluster Kube API Server Operator for OpenShift.
These tests run against OpenShift clusters and are meant to be used in the OpenShift CI/CD pipeline.
They use the framework: https://github.com/openshift-eng/openshift-tests-extension

## Quick Start
### Building the Test Extension

From the repository root:
```bash
make tests-ext-build
```

The binary will be located at: `cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests`

### Running Tests

| Command                                                                    | Description                                                              |
|----------------------------------------------------------------------------|--------------------------------------------------------------------------|
| `make tests-ext-build`                                                     | Builds the test extension binary.                           |
| `make run-suite SUITE=<suite-name> [JUNIT_DIR=<dir>]`                     | Runs a test suite (e.g., `SUITE=openshift/cluster-kube-apiserver-operator/conformance/parallel`). |
| `./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list`  | Lists all available test cases.                                          |
| `./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests run-suite <suite-name>` | Runs a test suite directly. |
| `./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests run-test <test-name>` | Runs one specific test. |

### Listing Suites and Tests

```bash
# List all available suites
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list suites

# List tests in a suite
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list tests --suite=openshift/cluster-kube-apiserver-operator/all

# Show suite info with qualifiers (filtering logic)
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests info | jq '.suites'

# Count tests in a suite
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list tests --suite=<suite-name> | jq 'length'

# Extract just test names
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list tests --suite=<suite-name> | jq -r '.[] | .name'
```

## Test Suite Organization

Tests are automatically distributed into different suites based on tags in the test names. This ensures proper test execution and prevents duplication.

### Available Suites

| Suite Name | Purpose |
|------------|---------|
| `openshift/cluster-kube-apiserver-operator/conformance/parallel` | Fast, parallel-safe tests |
| `openshift/cluster-kube-apiserver-operator/conformance/serial` | Serial execution tests |
| `openshift/cluster-kube-apiserver-operator/optional/slow` | Long-running and timeout tests |
| `openshift/cluster-kube-apiserver-operator/all` | All tests |

### Test Distribution Rules

Tests are distributed into suites based on tags in the test name:

1. **Parallel Suite**: Tests WITHOUT `[Serial]`, `[Slow]`, or `[Timeout:]` tags
   - Example: `[sig-api-machinery] sanity test should always pass`

2. **Serial Suite**: Tests WITH `[Serial]` tag but NOT `[Slow]` tag
   - Example: `[Serial][Disruptive] should update configuration`
   - Example: `[Serial][Timeout:30m] should complete eventually`

3. **Slow Suite**: Tests WITH `[Slow]` tag OR tests WITH `[Timeout:]` tag that are NOT `[Serial]`
   - Example: `[Slow][Serial][Timeout:90m] should configure eventTTLMinutes` (has `[Slow]`)
   - Example: `[Timeout:45m] should wait for long operation` (has `[Timeout:]` but no `[Serial]`)

**Note:** Each test runs in exactly one suite to avoid duplication.

### Common Test Tags

| Tag | Purpose |
|-----|---------|
| `[Serial]` | Must run sequentially |
| `[Slow]` | Long-running test |
| `[Timeout:XXm]` | Custom timeout (supports any duration like 30m, 45m, 90m, 120m, etc.) |
| `[Disruptive]` | Modifies cluster state (automatically tagged as `[Serial]`) |

## How to Run the Tests Locally

The tests can be run locally using the `cluster-kube-apiserver-operator-tests` binary against an OpenShift cluster.
Use the environment variable `KUBECONFIG` to point to your cluster configuration file such as:

```shell
export KUBECONFIG=path/to/kubeconfig
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests run-test <test-name>
```

### Local Test using OCP

1. Use the `Cluster Bot` to create an OpenShift cluster.

**Example:**

```shell
launch 4.20 gcp,techpreview
```

2. Set the `KUBECONFIG` environment variable to point to your OpenShift cluster configuration file.

**Example:**

```shell
mv ~/Downloads/cluster-bot-2025-08-06-082741.kubeconfig ~/.kube/cluster-bot.kubeconfig
export KUBECONFIG=~/.kube/cluster-bot.kubeconfig
```

3. Run the tests using the `cluster-kube-apiserver-operator-tests` binary.

**Example:**
```shell
./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests run-suite openshift/cluster-kube-apiserver-operator/all
```

Or using make from the root directory:
```shell
make run-suite SUITE=openshift/cluster-kube-apiserver-operator/all JUNIT_DIR=/tmp/junit-results
```

## Test Module Structure

The test extension uses a **single module approach** where test code and production code share the same `go.mod`:

```
├── cmd/cluster-kube-apiserver-operator-tests/              # Test extension main package
│   ├── main.go                                             # Test binary entry point
│   └── cluster-kube-apiserver-operator-tests               # Test binary (built here)
├── test/e2e/                                               # End-to-end tests
│   ├── event_ttl.go                                        # EventTTL tests
│   ├── doc.go                                              # Package documentation
│   └── README.md                                           # This file
├── go.mod                                                  # Single module for entire repo
└── Makefile                                                # Build targets including tests
```

### Key Benefits of Single Module Approach

- **Simpler dependency management**: All dependencies in one `go.mod`
- **Easier development**: No need to sync versions between modules
- **Better CI integration**: Uses existing CI jobs for test/e2e tests
- **Consistent build tooling**: Same build-machinery-go patterns as operator

### Dependency Management

All dependencies are managed in the root `go.mod`:

```go
module github.com/openshift/cluster-kube-apiserver-operator

require (
    github.com/onsi/ginkgo/v2 v2.22.0        // Test framework
    github.com/onsi/gomega v1.36.1           // Assertion library
    github.com/openshift-eng/openshift-tests-extension v0.0.0-... // OTE framework
    // ... other dependencies
)

replace github.com/onsi/ginkgo/v2 => github.com/openshift/onsi-ginkgo/v2 v2.6.1-0.20250416174521-4eb003743b54
```

## Writing Tests

You can write tests in the `test/e2e/` directory.

### Adding Test Tags

When writing tests, include appropriate tags in the test description to ensure correct suite distribution:

```go
// Fast parallel test (no tags needed)
g.It("should pass basic validation", func() {
    // test code
})

// Serial test
g.It("should modify cluster state [Serial][Disruptive]", func() {
    // test code
})

// Slow test with timeout
g.It("should complete long operation [Timeout:60m][Slow]", func() {
    // test code
})

// Serial test with timeout (goes to serial suite, not slow)
g.It("should wait for rollout [Serial][Timeout:30m]", func() {
    // test code
})
```

After adding or modifying tests, always run `make tests-ext-update` to update test metadata.

## Development Workflow

- Add or update tests in: `test/e2e/`
- Run `make build` to build the operator binary and `make tests-ext-build` for the test binary.
- You can run the full suite or one test using the commands in the table above.
- Before committing your changes:
    - Run `make tests-ext-update` (updates test metadata)
    - Run `make verify` to check formatting, linting, and validation

## How to Rename a Test

1. Run `./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list` to see the current test names
2. Find the name of the test you want to rename
3. Add a Ginkgo label with the original name, like this:

```go
It("should pass a renamed sanity check",
	Label("original-name:[sig-kube-apiserver] My Old Test Name"),
	func(ctx context.Context) {
		Expect(len("test")).To(BeNumerically(">", 0))
	})
```

4. Run `make tests-ext-update` to update the metadata

**Note:** Only add the label once. Do not update it again after future renames.

## How to Delete a Test

1. Run `./cmd/cluster-kube-apiserver-operator-tests/cluster-kube-apiserver-operator-tests list` to find the test name
2. Add the test name to the `IgnoreObsoleteTests` block in `cmd/cluster-kube-apiserver-operator-tests/main.go`, like this:

```go
ext.IgnoreObsoleteTests(
    "[sig-kube-apiserver] My removed test name",
)
```

3. Delete the test code from your suite.
4. Run `make tests-ext-update` to clean the metadata

**WARNING**: Deleting a test may cause issues with Sippy https://sippy.dptools.openshift.org/sippy-ng/
or other tools that expected the Unique TestID tracked outside of this repository. [More info](https://github.com/openshift-eng/ci-test-mapping)
Check the status of https://issues.redhat.com/browse/TRT-2208 before proceeding with test deletions.

## E2E Test Configuration

Tests are configured in the `openshift/release` repository, under `ci-operator/config/openshift/cluster-kube-apiserver-operator`.

Here is a CI job example:

```yaml
- as: e2e-aws-techpreview-ckas-op-ext
  steps:
    cluster_profile: aws
    env:
      FEATURE_SET: TechPreviewNoUpgrade
      TEST_SUITE: openshift/cluster-kube-apiserver-operator/all
    test:
    - ref: openshift-e2e-test
    workflow: openshift-e2e-aws
```

This uses the `openshift-tests` binary to run cluster-kube-apiserver-operator tests against a test OpenShift release.

It works for pull request testing because of this:

```yaml
releases:
  latest:
    integration:
      include_built_images: true
```

More info: https://docs.ci.openshift.org/docs/architecture/ci-operator/#testing-with-an-ephemeral-openshift-release

## Makefile Commands

### Root Makefile (from repository root)

| Target                   | Description                                                                  |
|--------------------------|------------------------------------------------------------------------------|
| `make build`             | Builds the operator binary.                                                      |
| `make tests-ext-build`   | Builds the test extension binary to `cmd/cluster-kube-apiserver-operator-tests/`.              |
| `make tests-ext-update`  | Updates test metadata.                         |
| `make tests-ext-clean`   | Cleans test extension binaries.                |
| `make run-suite SUITE=<name> [JUNIT_DIR=<dir>]` | Runs a test suite with optional JUnit XML output. |
| `make clean`             | Cleans both operator and test binaries.                                      |
| `make verify`            | Runs formatting, vet, and linter.                                            |

**Note:** Metadata is stored in: `cmd/cluster-kube-apiserver-operator-tests/.openshift-tests-extension/openshift_payload_cluster-kube-apiserver-operator.json`

## FAQ

### Why don't we have a Dockerfile for `cluster-kube-apiserver-operator-tests`?

We do not provide a Dockerfile for `cluster-kube-apiserver-operator-tests` because building and shipping a
standalone image for this test binary would introduce unnecessary complexity.

Technically, it is possible to create a new OpenShift component just for the
tests and add a corresponding test image to the payload. However, doing so requires
onboarding a new component, setting up build pipelines, and maintaining image promotion
and test configuration — all of which adds overhead.

From the OpenShift architecture point of view:

1. Tests for payload components are part of the product. Many users (such as storage vendors, or third-party CNIs)
rely on these tests to validate that their solutions are compatible and conformant with OpenShift.

2. Adding new images to the payload comes with significant overhead and cost.
It is generally preferred to include tests in the same image as the component
being tested whenever possible.

### Why do we need to run `make tests-ext-update`?

Running `make tests-ext-update` ensures that each test gets a unique and stable **TestID** over time.

The TestID is used to identify tests across the OpenShift CI/CD pipeline and reporting tools like Sippy.
It helps track test results, detect regressions, and ensures the correct tests are
executed and reported.

This step is important whenever you add, rename, or delete a test.
More information:
- https://github.com/openshift/enhancements/blob/master/enhancements/testing/openshift-tests-extension.md#test-id
- https://github.com/openshift-eng/ci-test-mapping

### Why use a single module instead of separate test module?

The single module approach:
- Simplifies dependency management (one `go.mod` to maintain)
- Integrates with existing CI jobs that run tests from `test/e2e/`
- Follows the pattern agreed upon by the team (see Slack discussion)
- Uses existing Makefile patterns with build-machinery-go
- Avoids complexity of syncing versions between modules

### How to get help with OTE?

For help with the OpenShift Tests Extension (OTE), you can reach out on the #wg-openshift-tests-extension Slack channel.
