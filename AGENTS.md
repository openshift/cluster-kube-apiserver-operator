# cluster-kube-apiserver-operator — AI Agent Guidelines

This file provides guidance to AI agents when working with code in this repository.

This is the **cluster-kube-apiserver-operator** — the OpenShift operator that manages the
Kubernetes API server (KAS) as static pods on control-plane nodes. Built on
[library-go](https://github.com/openshift/library-go) and installed via the
[Cluster Version Operator](https://github.com/openshift/cluster-version-operator).

## Design Goals

In priority order:

1. **Stability over features** — KAS is the most critical cluster component. Every change
   must be evaluated for rollout risk.
2. **Minimal dependencies** — one small library can pull in dozens of transitive packages
   ("one drop of water, but a river came"). Prefer stdlib and `library-go` first.
3. **Operational simplicity** — code that is easy to debug at 3 AM beats elegant but opaque code.
4. **Backward compatibility** — the operator from version N+1 may manage a KAS from version N
   during upgrade, and vice versa during rollback.

## Build & Test Commands

```bash
make build                    # Build operator + OTE test binaries
make test-unit                # Run unit tests (excludes e2e)
make verify                   # All verification checks (bindata, generated code)
make update                   # Regenerate all generated files
go mod tidy && go mod vendor  # Update vendored dependencies
```

E2E tests require a running OpenShift cluster with `KUBECONFIG` set:

```bash
make test-e2e                 # Run e2e tests (test/e2e/...)

# OTE: run a specific test
./cluster-kube-apiserver-operator-tests-ext run-test "test-name"
```

## Architecture

### Operator (`pkg/operator/`)
Watches the `KubeAPIServer` CR (`operator.openshift.io/v1`) and manages static-pod-based
KAS instances. Uses library-go's static pod operator framework.

### Static Pod Lifecycle — Critical Domain Knowledge
KAS runs as a **static pod** on each control-plane node. Config changes trigger a rolling
update ("rollout") managed by the `NodeInstaller` controller:

- Each rollout creates a new **revision** (monotonically increasing integer)
- A full rollout across 3 control-plane nodes takes **15–20 minutes**
- A harmless-looking config observer change can trigger an unexpected rollout

### Configuration Observers (`pkg/operator/configobservation/`)
Watch external resources (etcd, images, network, auth) and merge observed values into
KAS config. Each observer must:
- Handle missing/nil resources gracefully
- Be idempotent (calling twice produces the same result)
- Not collide config paths with other observers
- Return `existingConfig` on error (never return partial config)

### ClusterOperator Status
The operator reports health via `ClusterOperator/kube-apiserver` with conditions:
`Available`, `Progressing`, `Degraded`. Always include a human-readable `message` when
setting `Degraded` or `Progressing` — it appears in `oc get co` and is the first thing
an SRE sees during an incident.

### Key Namespaces
- `openshift-kube-apiserver-operator` — the operator deployment
- `openshift-kube-apiserver` — KAS static pods, configmaps, secrets (target namespace)
- `openshift-config` / `openshift-config-managed` — cluster-wide / operator-managed config

### Key Constants
- Namespace constants: `pkg/operator/operatorclient/`
- Bindata assets: `bindata/assets/kube-apiserver/`

## Anti-Patterns — Explicitly Forbidden

| Anti-Pattern | Why |
|---|---|
| Editing files under `vendor/` | Lost on next `go mod vendor`; fix upstream |
| Hardcoding namespace strings | Use `operatorclient.TargetNamespace` etc. |
| `time.Sleep` in operator code | Use `wait.PollImmediate` or controller requeue |
| `_ = foo()` (ignoring errors silently) | Return or log; silent failures are undebuggable |
| `context.TODO()` in production code | Pass proper context from caller |
| Mixing unrelated changes in one commit | Makes `git bisect` impossible |
| Adding dependencies without justification | See Dependency Policy below |
| Skipping `defer` cleanup in e2e tests | Leaves dirty cluster state |
| Copy-pasting client init boilerplate | Use shared helpers |
| `grep -P` in scripts | Fails on macOS; use `grep -E` or `rg` |

## AI Agent Behavior Rules

### Code Change Principles
- **Always explain WHY** — not just what changed, but the reasoning and problem context.
- **Prefer minimal diffs** — change only what's necessary; resist "cleanup while you're there."
- **No unrelated refactoring** — don't improve surrounding code unless explicitly requested.
- **Match existing style** — follow patterns in surrounding code.
- **When unsure, ASK** — ask clarifying questions instead of guessing.
- **Verify before submitting** — `make build && make test-unit && make verify`.

### OpenShift Reality Check
This operator runs in production OpenShift clusters managing critical control plane:
- Rollouts take 15–20 min and briefly disrupt control plane
- Bugs can prevent cluster upgrades or cause outages
- Code must handle: network partitions, etcd unavailability, node failures, operator restarts
- Resources may not exist: optional CRDs, optional operators, degraded cluster states

**Do NOT suggest code that:**
- Assumes perfect network conditions
- Ignores mid-operation operator restarts
- Requires manual intervention to recover
- Breaks during cluster upgrades
- Depends on resources that may not exist

## Dependency Policy

**Do NOT add new dependencies without strong justification.** Before adding one:
1. Can stdlib or `library-go` do it?
2. How many transitive deps? (`go mod graph | grep <dep> | wc -l`)
3. Is it actively maintained with a compatible license?

## PR / Commit Conventions

### Git Commit Structure
- Exactly **2 commits** (code + deps/generated) OR **1 commit** (docs-only)
- Commits based on `upstream/main`, not fork
- Generated artifacts in commit 2 only
- No merge commits or unrelated fork changes
- Verify: `git log --oneline upstream/main..HEAD`

**Code commits** — one or more commits with functional changes (operator logic, tests,
manifests). Exclude `go.mod`, `go.sum`, `vendor/`, and generated files.

**Generated/vendor commit (if needed)** — a single final commit. Message format:
- Vendor only: `vendor: bump(*)`
- Generated only: `update generated`
- Both: `vendor: bump(*), update generated`

### Commit Message Format
```
<component>: <short description>

<Why this change is needed. Jira/Bugzilla link if applicable.>
<Risk level (low/medium/high based on disruption potential).>
```

### PR Description Must Include
- **What** and **why** (Jira/Bugzilla link)
- **Testing strategy** — what tests run, coverage added
- **Impact analysis** — rollout triggers, disruption, upgrade considerations
- **Risk level** — Low (test/docs), Medium (config observer), High (static pod, certs, RBAC)
- **Dependency justification** — if adding deps
- **Feature gate** — depends on or interacts with a feature gate?

### PR Generation — Avoid
- Large refactors (small focused changes are safer)
- Mixed concerns (one problem per PR)
- Speculative improvements (don't add unrequested features)
- Formatting-only changes mixed with logic changes

### Conflict Resolution During Rebase
1. `go.mod`/`go.sum`: accept upstream, re-apply with `go get` + `go mod tidy`
2. `vendor/`: accept upstream, regenerate with `go mod vendor`
3. Generated files (`zz_generated.*`): accept upstream, re-run `make update`
4. Never manually resolve conflicts in vendor/ or generated files

## PR Review Checklist

**MANDATORY** — all PRs must pass:

### 1. Build and Tests
- [ ] `make build` succeeds
- [ ] `make test-unit` passes
- [ ] `make verify` passes
- [ ] New/modified code has unit tests
- [ ] E2E tests updated if affecting rollout/certificates/SA tokens

### 2. Git Commit Structure
- [ ] Exactly 2 commits (code + deps/generated) OR 1 commit (docs-only)
- [ ] Commits based on `upstream/main`, not fork
- [ ] Generated artifacts in commit 2 only
- [ ] No merge commits or unrelated fork changes
- [ ] Verify: `git log --oneline upstream/main..HEAD`

### 3. Dependency Changes
- [ ] New dependency strongly justified (why can't stdlib/client-go/copying suffice?)
- [ ] Transitive impact measured: `go mod graph | grep <package>`
- [ ] Binary size impact acceptable
- [ ] `go mod tidy && go mod vendor` produces no diff
- [ ] No edits under `vendor/` (upstream PR first)
- [ ] Deps committed separately in commit 2

### 4. Go Conventions
- [ ] No duplicate constants — extract to shared location
- [ ] No repeated boilerplate — use/create helpers
- [ ] Errors returned up chain (operator code) or `require.NoError` (tests)
- [ ] No hardcoded namespaces — use `operatorclient.TargetNamespace`

### 5. E2E Test Changes
- [ ] Correct tags: `[Operator]`, `[Serial]`, `[Disruptive]`, `[Conformance]`
- [ ] Timeouts account for rollouts (≥20 min per rollout)
- [ ] Uses shared helpers (not copy-paste boilerplate)
- [ ] `defer` cleanup + stability wait before exit
- [ ] Constants not duplicated across files

### 6. Config Observer Changes
- [ ] Registered in `pkg/operator/start.go`
- [ ] Handles missing/nil resources gracefully
- [ ] Config paths don't collide with other observers
- [ ] Unit tests: happy path, missing resource, malformed, idempotent

### 7. Static Pod / Manifest Changes
- [ ] `bindata/assets/` changes reflected in `pkg/operator/targetconfigcontroller/`
- [ ] `make verify-bindata-v4.1.0` passes
- [ ] Doesn't break bootstrap rendering (`pkg/cmd/render/`)
- [ ] Security changes (RBAC, volumes) justified in PR description

### 8. Operator Controller Changes
- [ ] Requeues on transient errors (no tight loops)
- [ ] No blocking on long operations (use async patterns)
- [ ] ClusterOperator conditions updated correctly
- [ ] Uses exponential backoff with jitter for retries

## Testing Constraints

### Unit Tests
- **Table-driven tests** are the standard pattern with descriptive `name` fields.
- Cover: happy path, error path, nil/empty input, idempotency.
- Config observer tests **must** verify `existingConfig` is returned on error.

### E2E Tests — Critical Rules
1. **Disruption budget** — each KAS rollout = 15–20 min + brief API unavailability per node.
   Two rollouts = minimum 40 min timeout.
2. **Pre-condition checks** — verify cluster stability before introducing disruption.
3. **Cleanup in `defer`** — always. Include stability waits inside the defer.
4. **Monitor compatibility** — CI monitors flag pathological events. No aggressive pod
   deletion loops; prefer one-shot remediation with explicit health waits.
5. **No assumptions** — don't assume revision numbers, pod counts, or timing.
   Use `wait.PollImmediate`, not `time.Sleep`.
6. **Ginkgo tags** — `[Operator][Serial]`, `[Disruptive]`, `[Conformance][Serial]`
7. **OTE format** — new tests go in `test/e2e/<name>.go` + `test/e2e/<name>_test.go`

### Feature Gate Testing
- Use `featuregates.NewHardcodedFeatureGateAccess` in unit tests
- Test both enabled and disabled paths
- Feature gates defined in `openshift/api/features/features.go`
- Render path (`pkg/cmd/render/`) and runtime observer must handle the same gates consistently

## Domain-Specific Patterns

### Retry/Backoff
- **Controllers**: return error from `Sync()` → framework retries with backoff. No custom loops.
- **E2E tests**: `wait.PollImmediate(interval, timeout, fn)` with appropriate values:
  - API object creation: 5s / 1–2 min
  - KAS pod rollout: 30s / 60 min
  - ClusterOperator stability: 60s / 60 min

### Feature Gates
```go
if featureGates.Enabled(features.FeatureGateFoo) {
    // new behavior
} else {
    // existing behavior — must remain functional (safe fallback)
}
```

### Stability Checks After Rollouts
- `WaitForAPIServerToStabilizeOnTheSameRevision` — checks pod revision convergence only.
  Does **not** verify ClusterOperator status. `NodeInstallerProgressing` may still be true.
- For robust checks, also verify `ClusterOperator/kube-apiserver` conditions
  (Available=True, Progressing=False, Degraded=False).

### SA Token / Signing Key Rotation
- Projected SA tokens have **1-hour default expiry**
- After signing key rotation, every operator using a projected SA token will crash-loop
  with `Unauthorized` until it gets a token signed by the new key
- Recovery via Kubernetes backoff can take **30–60 minutes**
- Bounce crash-looping pods (one-shot delete) to force fresh token injection, then
  wait for health before checking ClusterOperator stability

### Observability
- Operator code: `klog.Infof` / `klog.Errorf` with context (namespace, resource, conditions)
- Tests: `t.Logf`
- Alerts: `bindata/assets/alerts/` — include `severity`, `for` duration, `runbook_url`
- ClusterOperator `message` field — always set when Degraded or Progressing

## Code Style
- Uses `openshift/library-go` controller framework, not raw controller-runtime
- Error handling: return errors up the chain in operator code; `require.NoError` in tests
- Namespace references via `operatorclient` constants, never string literals
- Generated file: `pkg/operator/v4_00_assets/bindata.go` — never hand-edit
