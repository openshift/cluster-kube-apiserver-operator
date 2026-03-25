This file provides guidance to AI agents when working with code in this repository.

This is the **cluster-kube-apiserver-operator** repository — the OpenShift operator that manages and
updates the [Kubernetes API server](https://github.com/kubernetes/kubernetes) deployed on
[OpenShift](https://openshift.io). It is based on the OpenShift
[library-go](https://github.com/openshift/library-go) framework and installed via the
[Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

## Key Architecture Components

### Operator
The main operator binary (`cmd/cluster-kube-apiserver-operator/`) watches the `KubeAPIServer`
custom resource (`operator.openshift.io/v1`) and manages static-pod-based Kubernetes API server
instances across control-plane nodes. It uses library-go's static pod operator framework.

### Static Pod Lifecycle
KAS runs as a **static pod** on each control-plane node. Changes trigger a rolling update
(called a "rollout") managed by the `NodeInstaller` controller. Each rollout creates a new
**revision** — a monotonically increasing integer stored as a label (`revision`) on the KAS pods.
A full rollout across 3 control-plane nodes typically takes **15–20 minutes**.

### Configuration Observers
Observers in `pkg/operator/configobservation/` watch external resources (etcd endpoints, image
config, network config, auth config, etc.) and merge observed values into the KAS configuration.

### Bindata Assets
Static assets (pod manifests, configs, alerts) live in `bindata/assets/`. The operator embeds
these and renders them during installation.

### ClusterOperator Status
The operator reports health via the `ClusterOperator/kube-apiserver` resource with three
conditions: `Available`, `Progressing`, `Degraded`. The `NodeInstallerProgressing` message
indicates static pod updates are still in progress on nodes.

## Repository Layout

```
cmd/
  cluster-kube-apiserver-operator/         # Main operator binary
  cluster-kube-apiserver-operator-tests-ext/ # OTE test binary
pkg/
  operator/                                # Core operator controllers
    configobservation/                     # Config observer implementations
    certrotationcontroller/                # Certificate rotation
    targetconfigcontroller/                # Target config rendering
  cmd/
    render/                                # Bootstrap manifest renderer
    checkendpoints/                        # Network connectivity checker
  recovery/                               # API server recovery logic
test/
  e2e/                                     # E2E tests (Ginkgo-based, OTE-compatible)
  e2e-encryption/                          # Encryption-at-rest e2e tests
  e2e-encryption-rotation/                 # Encryption key rotation tests
  e2e-sno-disruptive/                      # Single-node disruptive tests
  library/                                 # Shared e2e test helpers
bindata/assets/                            # Embedded static assets (pods, configs, alerts)
manifests/                                 # CVO-managed manifests (deployment, RBAC, etc.)
vendor/                                    # Vendored dependencies (committed to repo)
```

## Common Development Commands

### Building

```bash
make build                    # Build operator and test binaries
make clean                    # Remove build artifacts
```

### Unit Tests

```bash
make test-unit                # Run unit tests (pkg/... and cmd/... only, excludes e2e)
go test ./pkg/...             # Run tests for a specific package tree
go test -v -run TestFoo ./pkg/operator/...  # Run a specific test
```

### E2E Tests

E2E tests require a running OpenShift cluster with `KUBECONFIG` set.

```bash
make test-e2e                 # Run all e2e tests (test/e2e/...)
make test-e2e-encryption      # Run encryption e2e tests (very slow, ~4h)
make test-e2e-sno-disruptive  # Run SNO disruptive tests
```

### OTE (OpenShift Tests Extension)

```bash
# Build the OTE test binary
make build

# List available test suites
./cluster-kube-apiserver-operator-tests-ext list suites

# List tests in a suite
./cluster-kube-apiserver-operator-tests-ext list tests \
  --suite=openshift/cluster-kube-apiserver-operator/operator/serial

# Run a suite (serial, concurrency=1)
./cluster-kube-apiserver-operator-tests-ext run-suite \
  openshift/cluster-kube-apiserver-operator/operator/serial -c 1

# Run a specific test by name
./cluster-kube-apiserver-operator-tests-ext run-test "test-name"

# With JUnit output
./cluster-kube-apiserver-operator-tests-ext run-suite \
  openshift/cluster-kube-apiserver-operator/operator/serial \
  --junit-path=/tmp/junit.xml
```

### Verification

```bash
make verify                   # Run all verification checks
make verify-bindata-v4.1.0    # Verify bindata assets are current
```

## Dependency Management

This repository uses **Go modules with vendoring**. All dependencies are committed in `vendor/`.

### Updating a Dependency

```bash
# Update a single dependency
go get github.com/openshift/library-go@latest
go mod tidy
go mod vendor

# Verify everything builds
make build
make test-unit
```

### Rebasing / Bumping library-go

The most common rebase is updating `library-go`, which provides the static pod operator
framework, test helpers, and shared controllers:

```bash
go get github.com/openshift/library-go@<commit-or-branch>
go mod tidy
go mod vendor
make build
make test-unit
```

After bumping, check for:
- **Breaking API changes** in library-go controllers used in `pkg/operator/`
- **Updated test helpers** in `vendor/github.com/openshift/library-go/test/library/` that may
  change default parameters (e.g., `WaitForAPIServerToStabilizeOnTheSameRevision` defaults)
- **New or changed controller interfaces** that require updates in `pkg/operator/start.go`

### Rebasing openshift/api

```bash
go get github.com/openshift/api@<commit-or-branch>
go mod tidy
go mod vendor

# If CRDs changed, update bindata
make update-bindata-v4.1.0
make verify-bindata-v4.1.0
```

## Common Agent Tasks

### Bug Fixes

1. **Identify the affected code** — most operator logic is in `pkg/operator/`.
2. **Write or update unit tests** — test files live alongside the code (`*_test.go`).
3. **Run `make test-unit`** to verify locally.
4. **Check for e2e test impact** — if the fix affects rollout behavior, SA tokens, or
   certificate rotation, update tests in `test/e2e/`.

### Backports

When backporting a fix to a release branch:

1. Cherry-pick the commit: `git cherry-pick <sha>`
2. Resolve any conflicts — pay attention to vendored dependency differences between branches.
3. Ensure `go mod tidy && go mod vendor` produces a clean state.
4. Run `make build && make test-unit`.

### Adding or Modifying Config Observers

Config observers follow a consistent pattern in `pkg/operator/configobservation/`:

1. Create the observer function matching `configobserver.ObserveConfigFunc` signature.
2. Register it in the observer list in `pkg/operator/start.go` or the relevant controller setup.
3. Add unit tests using `configobservation` test helpers.
4. The observer should return the observed config path and the observed value.

### Adding E2E Tests

E2E tests use **Ginkgo v2** with **OTE** integration:

1. Create test logic in `test/e2e/<name>.go` (helper functions).
2. Create the Ginkgo test file in `test/e2e/<name>_test.go` with appropriate tags:
   - `[Operator]` + `[Serial]` — included in the operator serial suite
   - `[Disruptive]` — also included in the operator serial suite
   - `[Conformance]` + `[Serial]` — included in the conformance serial suite
3. Use shared helpers from `test/library/` and `vendor/.../library-go/test/library/`.
4. If the test triggers KAS rollouts, account for 15–20 min per rollout in timeouts.

### Modifying Static Pod Manifests

1. Edit templates in `bindata/assets/kube-apiserver/`.
2. If the change affects the pod spec, update `pkg/operator/targetconfigcontroller/`.
3. Run `make verify-bindata-v4.1.0` to ensure bindata is in sync.

## Code Patterns and Conventions

### Client Initialization in E2E Tests

Use helper functions to reduce boilerplate:

```go
func newTestCoreV1Client(t testing.TB) *clientcorev1.CoreV1Client {
    kubeConfig, err := libgotest.NewClientConfigForTest()
    require.NoError(t, err)
    kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
    require.NoError(t, err)
    return kubeClient
}
```

### Shared Constants

When multiple e2e test files need the same constants (intervals, timeouts), define them once
and import from a shared location rather than duplicating across files.

### Stability Checks After Rollouts

When waiting for KAS rollouts to complete in tests:

- `WaitForAPIServerToStabilizeOnTheSameRevision` — checks that all `apiserver=true` pods
  converge to the same revision label. Uses library-go defaults (6 checks, 1 min interval,
  22 min timeout).
- `WaitForPodsToStabilizeOnTheSameRevision` — generic version allowing custom parameters.
- **Important:** These check *running pod revision labels* only. They do not verify
  `ClusterOperator` status, so `NodeInstallerProgressing` may still be true after they pass.
  For more robust checks, also verify `ClusterOperator/kube-apiserver` conditions.

### Error Handling

- Use `require.NoError(t, err)` in tests for fatal assertions.
- Use `assert.NoError(t, err)` only when the test should continue after failure.
- Operator code should return errors up the call chain; controllers handle retries via requeueing.

## CI and Testing Infrastructure

CI jobs are defined externally in the [openshift/release](https://github.com/openshift/release)
repository, not in this repo. The CI runs:
- Unit tests (`make test-unit`)
- Verification checks (`make verify`)
- E2E tests against ephemeral OpenShift clusters
- The OTE serial suite on pull requests

## Key Dependencies

| Dependency | Purpose |
|-----------|---------|
| `openshift/library-go` | Static pod operator framework, test helpers, shared controllers |
| `openshift/api` | OpenShift API type definitions, CRDs |
| `openshift/client-go` | Generated OpenShift API clients |
| `openshift/build-machinery-go` | Shared Makefile targets and build tooling |
| `onsi/ginkgo/v2` | E2E test framework |
| `openshift-eng/openshift-tests-extension` | OTE test binary framework |

## Important Namespaces

| Namespace | Contains |
|-----------|----------|
| `openshift-kube-apiserver-operator` | The operator deployment |
| `openshift-kube-apiserver` | KAS static pods, configmaps, secrets (target namespace) |
| `openshift-config` | Cluster-wide configuration resources |
| `openshift-config-managed` | Operator-managed configuration |

## PR Review Guidelines

When reviewing pull requests to this repository (as an AI agent or assisted reviewer),
follow this checklist systematically.

### Pre-Review Steps

```bash
# Fetch the PR locally
gh pr checkout <pr-number>

# Build and run unit tests
make build
make test-unit

# Run verification
make verify
```

### Code Quality Checklist

**MANDATORY** — every PR must pass all of the following:

#### 1. Build and Tests
- [ ] `make build` succeeds without errors
- [ ] `make test-unit` passes
- [ ] `make verify` passes (includes bindata, generated code checks)
- [ ] New or modified code has corresponding unit tests
- [ ] E2E tests updated if the change affects rollout, certificate, or SA token behavior

#### 2. Go Conventions
- [ ] No duplicate constants or variables across files — extract to shared locations
- [ ] No repeated boilerplate patterns — use or create helper functions
- [ ] Error handling follows the chain: return errors in operator code, `require.NoError` in tests
- [ ] No unused imports or variables (compiler will catch, but verify)
- [ ] Comments explain *why*, not *what* — avoid narrating obvious code

#### 3. Dependency Changes
- [ ] `go.mod` and `go.sum` are consistent (`go mod tidy` produces no diff)
- [ ] `vendor/` is up to date (`go mod vendor` produces no diff)
- [ ] No direct edits to files under `vendor/`
- [ ] Breaking changes from dependency bumps are addressed in consuming code
- [ ] If `openshift/api` changed, `make update-bindata-v4.1.0` was run

#### 4. E2E Test Changes
- [ ] Tests use correct Ginkgo tags (`[Operator]`, `[Serial]`, `[Disruptive]`, `[Conformance]`)
- [ ] Timeouts account for KAS rollout duration (15–20 min per rollout)
- [ ] Client initialization uses shared helpers (e.g., `newTestCoreV1Client`) not copy-pasted boilerplate
- [ ] Constants (intervals, timeouts) are not duplicated across test files
- [ ] Stability checks are appropriate — `WaitForAPIServerToStabilizeOnTheSameRevision` for
  basic revision checks, `ClusterOperator` condition checks for full rollout verification
- [ ] `defer` cleanup blocks restore cluster state (delete test resources, wait for stability)

#### 5. Config Observer Changes
- [ ] Observer is registered in `pkg/operator/start.go`
- [ ] Observer handles missing/nil resources gracefully (cluster may not have the resource)
- [ ] Observed config paths are correct and do not collide with other observers
- [ ] Unit tests cover: happy path, missing resource, malformed resource, no-change (idempotent)

#### 6. Static Pod / Manifest Changes
- [ ] `bindata/assets/` changes are reflected in `pkg/operator/targetconfigcontroller/`
- [ ] `make verify-bindata-v4.1.0` passes
- [ ] Pod manifest changes do not break bootstrap rendering (`pkg/cmd/render/`)
- [ ] Security-sensitive changes (RBAC, service accounts, volumes) are justified in the PR description

#### 7. Operator Controller Changes
- [ ] Controller properly requeues on transient errors
- [ ] Controller does not block on long operations — use async patterns
- [ ] `ClusterOperator` conditions are updated correctly (Available, Progressing, Degraded)
- [ ] No hardcoded namespace strings — use constants from `operatorclient` package

### Review Focus Areas by Change Type

| Change Type | Key Review Areas |
|------------|------------------|
| Config observer | Idempotency, nil handling, config path collisions, unit test coverage |
| Certificate rotation | Expiry timing, signer chain, CA bundle updates, e2e coverage |
| SA token / signing key | Token expiry (1h default), operator crash-loop recovery, rollout timing |
| Static pod manifest | Bootstrap compatibility, resource limits, volume mounts, security context |
| Dependency bump | Breaking API changes, test helper parameter changes, vendor consistency |
| E2E test | Timeout adequacy, tag correctness, cleanup in defer, shared helpers |
| Alert rules | PromQL correctness, thresholds, `for` duration, runbook links |

### Common Review Findings

These are frequently flagged issues — check for them proactively:

1. **Duplicate constants** — same `interval`, `timeout` values defined in multiple test files.
   Extract to a shared location or reuse existing constants.
2. **Repeated client initialization** — `NewClientConfigForTest()` + `NewForConfig()` pattern
   copy-pasted across functions. Use or extend helper functions like `newTestCoreV1Client`.
3. **Insufficient rollout timeouts** — tests that trigger KAS rollouts but use short timeouts
   (< 20 min). Each rollout takes 15–20 min; key rotation triggers 2 rollouts.
4. **Missing `ClusterOperator` checks** — relying solely on pod revision checks which can pass
   while `NodeInstallerProgressing` is still true.
5. **Hardcoded namespaces** — using string literals like `"openshift-kube-apiserver"` instead
   of `operatorclient.TargetNamespace`.
6. **Editing vendored code** — changes under `vendor/` that should be upstream PRs instead.
7. **Missing test cleanup** — test resources not deleted in `defer` blocks, leaving cluster
   in a dirty state for subsequent tests.

### PR Description Expectations

A good PR description for this repo should include:
- **What** changed and **why** (link to Jira/Bugzilla if applicable)
- **Which components** are affected (operator, config observer, e2e tests, manifests)
- **Rollout impact** — does this trigger a KAS rollout? How many?
- **Test plan** — how was this verified? (unit tests, e2e suite, manual cluster testing)
- **Upgrade/downgrade considerations** — is the change backward compatible?

## Common Pitfalls

- **macOS `grep -P`**: Scripts using `grep -P` (Perl regex) will fail on macOS which only has
  BSD grep. Use `grep -E` (extended regex) or install GNU grep (`ggrep`) instead.
- **Vendored code**: Never edit files under `vendor/` directly. Update the source dependency
  and re-vendor with `go mod vendor`.
- **Static pod rollout timing**: KAS rollouts take 15–20 minutes per rollout. Tests that
  trigger multiple rollouts (e.g., key rotation) need proportionally higher timeouts.
- **Projected SA token expiry**: Projected service account tokens mounted in pods have a
  default 1-hour expiry. After signing key rotation, the operator pod may crash-loop with
  `Unauthorized` errors until it obtains a token signed with the new key.
