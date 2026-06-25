# Cluster Kube API Server Operator

A static pod operator that manages the lifecycle of `kube-apiserver` on OpenShift control plane nodes. Built on the [library-go](https://github.com/openshift/library-go) static pod operator framework, it observes cluster configuration, rotates certificates, manages encryption at rest, and reconciles the target kube-apiserver config into static pod manifests. Installed by the [Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full design and data flow.

## Build and Test

```bash
make build                       # Build all binaries (operator + OTE test runner)
make test                        # Unit tests (./pkg/... ./cmd/...)
make verify                      # Formatting, vetting, golang version checks
make test-e2e                    # E2E operator tests (3h timeout, serial)
make test-e2e-encryption-aescbc  # Encryption tests with aescbc provider (4h)
make test-e2e-encryption-kms     # KMS encryption tests (4h)
```

Go version: see `go.mod`.

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/cluster-kube-apiserver-operator/` | Operator binary entry point (operator, render, installer, pruner, startup-monitor, cert controllers) |
| `cmd/cluster-kube-apiserver-operator-tests-ext/` | OTE test runner entry point |
| `pkg/operator/starter.go` | Operator initialization — creates clients, informers, and starts all controllers |
| `pkg/operator/targetconfigcontroller/` | Renders observed config + defaults into kube-apiserver ConfigMaps/Secrets |
| `pkg/operator/configobservation/` | Configuration observers — each subdirectory watches a cluster resource type |
| `pkg/operator/certrotationcontroller/` | Certificate rotation for serving, LB, aggregator, and kubelet certs |
| `pkg/operator/resourcesynccontroller/` | Syncs ConfigMaps/Secrets between namespaces |
| `pkg/operator/operatorclient/` | Namespace constants and operator client interfaces |
| `pkg/cmd/render/` | Bootstrap manifest renderer for cluster installation |
| `pkg/recovery/` | Disaster recovery API server pod generation |
| `bindata/` | Embedded assets: default config, static pod template, alerts, RBAC, bootstrap manifests |
| `manifests/` | CVO deployment manifests (namespace, deployment, RBAC, ServiceMonitors) |
| `test/e2e*/` | E2E test suites (operator, encryption, encryption-rotation, encryption-perf, KMS, SNO) |
| `test/library/` | Shared test utilities |

## Controller Pattern

Controllers use the library-go `factory.Controller` base. Each controller has a `sync(ctx, syncContext)` method called by the framework on informer events or periodic resyncs. The operator wires them in `pkg/operator/starter.go` via `RunOperator()`.

Config observers follow a specific pattern: each observer function receives the existing config and returns `(observedConfig, errors)`. Observers are registered in `pkg/operator/configobservation/configobservercontroller/observe_config_controller.go`.

## Key Conventions

- **Namespaces:** `openshift-kube-apiserver-operator` (operator), `openshift-kube-apiserver` (operand), `openshift-config` (user config), `openshift-config-managed` (platform config). Constants in `pkg/operator/operatorclient/interfaces.go`.
- **Logging:** `k8s.io/klog/v2` with verbosity levels
- **Error handling:** wrap with `fmt.Errorf("context: %w", err)`
- **Feature gates:** controllers that depend on feature gates use `FeatureGateAccessor` from library-go; wait for gates before starting
- **Cert rotation:** `Refresh` should be ~80% of `Validity`. The `certificates.openshift.io/refresh-period` annotation is informational — actual rotation is decided by `notBefore`/`notAfter` on the secret. Cert rotation logic lives in `library-go/pkg/operator/certrotation/`.
- **API enablement:** `defaultGroupVersionsByFeatureGate` in `pkg/operator/configobservation/apienablement/` maps feature gates to API GroupVersions for `--runtime-config`. The vendored kube version is `k8s.io/component-base/version.DefaultKubeBinaryVersion`. Do not use `Scheme.PrioritizedVersionsForGroup` for version ordering — it returns registration order, not semantic order.
- **Upstream changes:** controllers that wrap library-go functionality should have fixes made upstream in [library-go](https://github.com/openshift/library-go), not here

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for full guidelines. Key rules:

- Do not modify files under `vendor/`. Use `go mod tidy && go mod vendor`.
- `bindata/assets.go` uses Go's `embed` directive to embed asset files — update the embedded files, not this file.
- The `apirequestcounts` CRD in bindata is copied from `vendor/`; use `make update-bindata-v4.1.0` to refresh and `make verify-bindata-v4.1.0` to check.
- Write unit tests for every change. E2E tests for significant features.
- Backwards compatibility matters — deprecate before removing.

## Testing

- **Unit tests:** co-located `*_test.go` files, table-driven, `go test ./pkg/... ./cmd/...`
- **E2E tests:** suites under `test/e2e*/`, each with its own Makefile target. `test/e2e/` and `test/e2e-encryption-kms/` use Ginkgo v2; the rest use standard Go testing.
- **OTE framework:** `cluster-kube-apiserver-operator-tests-ext` binary. See [CONTRIBUTING.md](CONTRIBUTING.md#openshift-tests-extension-ote) for usage.
