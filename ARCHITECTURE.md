# Architecture

## Overview

The cluster-kube-apiserver-operator is a static pod operator that manages the `kube-apiserver` on OpenShift control plane nodes. It is deployed by the Cluster Version Operator (CVO) and uses the [library-go](https://github.com/openshift/library-go) static pod operator framework.

The operator's primary responsibilities:
- Observe cluster configuration from multiple sources and synthesize kube-apiserver config
- Manage kube-apiserver static pods across control plane nodes (install, revision, prune)
- Rotate certificates (serving, load balancer, aggregator, kubelet)
- Manage encryption at rest (secrets, configmaps) with optional KMS support
- Report status via the `ClusterOperator/kube-apiserver` resource

## Data Flow

```text
 config.openshift.io resources          Secrets/ConfigMaps
 (APIServer, Authentication, Network,   (etcd certs, SA signing keys,
  Images, Infrastructure, Nodes, ...)    user-provided CAs, ...)
              │                                    │
              ▼                                    ▼
   ┌──────────────────────────────────────────────────┐
   │              Config Observer Controllers          │
   │  (observe external state, produce observedConfig) │
   └──────────────────────┬───────────────────────────┘
                          │ observedConfig (sparse JSON)
                          ▼
   ┌──────────────────────────────────────────────────┐
   │           Target Config Controller                │
   │  (merge defaults + observedConfig + overrides     │
   │   → render ConfigMaps/Secrets in target ns)       │
   └──────────────────────┬───────────────────────────┘
                          │ ConfigMaps, Secrets
                          ▼
   ┌──────────────────────────────────────────────────┐
   │          Static Pod Controllers (library-go)      │
   │  Installer → Revision Controller → Pruner         │
   │  (roll out new revisions to each control plane    │
   │   node as static pod manifests)                   │
   └──────────────────────┬───────────────────────────┘
                          │
                          ▼
              kube-apiserver static pods
              (one per control plane node)
```

## Operator Startup

Entry point: `cmd/cluster-kube-apiserver-operator/main.go` → `pkg/cmd/operator/cmd.go` → `pkg/operator/starter.go:RunOperator()`.

Startup sequence:
1. Create clients (Kubernetes, dynamic, config, security, operator control plane, API extensions, migration)
2. Create informers for watched namespaces (see [Namespaces](#namespaces))
3. Detect infrastructure topology (single-replica, dual-replica, HA) for conditional behavior
4. Initialize feature gates via `FeatureGateAccessor` and wait for observation (1-minute timeout)
5. Create and start all controllers concurrently
6. Block until context cancellation

## Namespaces

| Namespace | Constant | Purpose |
|-----------|----------|---------|
| `openshift-config` | `GlobalUserSpecifiedConfigNamespace` | User-provided configuration (certs, CAs, auth config) |
| `openshift-config-managed` | `GlobalMachineSpecifiedConfigNamespace` | Platform-managed configuration (generated CAs, signing certs) |
| `openshift-kube-apiserver-operator` | `OperatorNamespace` | Operator deployment and its resources |
| `openshift-kube-apiserver` | `TargetNamespace` | Operand: kube-apiserver pods, config, certs |

The `ResourceSyncController` copies ConfigMaps and Secrets between these namespaces as needed (e.g. etcd certs from `openshift-config` to the target namespace).

## Static Pod Management

The operator uses library-go's `staticpod.NewBuilder()` to manage kube-apiserver static pods. This framework provides:

- **Installer controller** — creates new static pod revisions on each control plane node. Uses a custom installer command (`cluster-kube-apiserver-operator installer`) with error injection support for testing.
- **Revision controller** — tracks revisions of ConfigMaps and Secrets. When any revisioned resource changes, a new revision is created. The first ConfigMap in the list (`kube-apiserver-pod`) contains the static pod manifest template.
- **Pruner** — removes old static pod revisions to free disk space.
- **PDB guard** — ensures availability during upgrades (only on multi-node clusters; disabled for single-node).
- **Min ready duration** — waits 30 seconds before considering a pod ready.
- **Startup monitor** — tracks kube-apiserver startup progress, with KMS-awareness.

Resources are split into two categories:
- **Revisioned** — ConfigMaps and Secrets that trigger a new revision when changed (config, pod manifest, certs, audit policies, encryption config).
- **Unrevisioned certs** — ConfigMaps and Secrets managed by `WithUnrevisionedCerts` that are updated in-place without triggering a revision (CA bundles, serving certs, client certs, signing keys, user-provided serving certs). See `CertConfigMaps` and `CertSecrets` in `starter.go` for the full list.

## Configuration Observers

Configuration observers watch external cluster resources and produce a sparse JSON config (`observedConfig`) that gets merged into the kube-apiserver configuration. Each observer function receives the existing config and returns `(observedConfig, errors)`.

Observers are registered in `pkg/operator/configobservation/configobservercontroller/observe_config_controller.go`. They are organized by the resource type they watch:

| Package | Watches | Config paths set |
|---------|---------|-----------------|
| `apiserver/` | `APIServer` CR | Named certificates, CORS, TLS profile, shutdown delay, graceful termination, admission plugins, event TTL, GOAWAY chance |
| `auth/` | `Authentication` CR | Auth metadata, SA issuer, webhook authenticator, external OIDC, pod security enforcement |
| `etcdendpoints/` | etcd endpoints in `openshift-etcd` | `etcd-servers` |
| `images/` | `Image` CR | Internal/external registry hostnames, allowed registries for import |
| `network/` | `Network` CR | Restricted CIDRs, services subnet, external IP policy, NodePort range |
| `node/` | `Node` CR | Minimum kubelet version, authorization modes, latency profile |
| `scheduler/` | `Scheduler` CR | Default node selector |
| `apienablement/` | `FeatureGate` CR | `runtime-config`, `feature-gates` (Kubernetes API enablement) |

Several observers are feature-gated and only activate when the corresponding feature gate is enabled.

## Target Config Controller

`pkg/operator/targetconfigcontroller/` takes the merged configuration (defaults + observedConfig + unsupportedConfigOverrides) and renders it into concrete resources in the target namespace:

- `config` ConfigMap — the main kube-apiserver configuration
- `kube-apiserver-pod` ConfigMap — the static pod manifest template
- CA bundle and server CA ConfigMaps
- Recovery serving cert and client token Secrets

The target config controller validates that required configuration is present (etcd servers, certs, admission plugins) before rendering.

## Certificate Rotation

`pkg/operator/certrotationcontroller/` manages certificate rotation for:

- **Localhost serving cert** — HTTPS serving on localhost
- **Service network serving cert** — HTTPS serving on the service network
- **External load balancer cert** — HTTPS serving for external LB (dynamically tracks hostnames)
- **Internal load balancer cert** — HTTPS serving for internal LB (dynamically tracks hostnames)
- **Aggregator client cert** — client cert for aggregated API servers
- **Kubelet client cert** — client cert for kubelet communication

Load balancer certificates use dynamic hostname tracking (`externalloadbalancer.go`, `internalloadbalancer.go`) to add SANs as new endpoints appear.

A separate `CertRotationTimeUpgradeableController` blocks cluster upgrades if certificates are about to expire during the upgrade window.

## Encryption

The operator manages encryption at rest for `secrets` and `configmaps` resources using library-go's encryption framework:

- **Providers** — supports `aescbc` and `aesgcm` encryption providers, plus optional KMS
- **Deployer** — `RevisionLabelPodDeployer` deploys encryption config via static pod revisions
- **Migrator** — uses `kube-storage-version-migrator` to re-encrypt existing resources when keys rotate
- **KMS support** — dedicated commands for KMS health checking (`kms-health`) and preflight validation (`kms-preflight`)

## Recovery

`pkg/recovery/` provides a disaster recovery mechanism that creates a temporary recovery kube-apiserver pod:

- Copies the main kube-apiserver pod manifest and modifies it for recovery
- Generates short-lived (7-day) self-signed certificates
- Creates an admin kubeconfig for recovery access
- Stores recovery resources in `{StaticPodResourcesDir}/recovery-kube-apiserver-pod/`

## Render Command

`pkg/cmd/render/` is a bootstrap manifest renderer used during cluster installation. It takes installer-provided inputs (etcd URLs, images, cluster CIDRs, feature gates) and renders the initial set of manifests needed to bootstrap kube-apiserver before the operator is running. The templates live in `bindata/bootkube/`.

## Other Controllers

| Controller | Purpose |
|-----------|---------|
| `StaticResourceController` | Applies static manifests from `bindata/` (namespace, service, RBAC, alerts, network policies) |
| `ClusterOperatorStatus` | Reports operator status, versions, and related objects to `ClusterOperator/kube-apiserver` |
| `ConnectivityCheckController` | Validates API server endpoint connectivity from pods |
| `KubeletVersionSkewController` | Validates kubelet version compatibility with the API server |
| `BoundSATokenSignerController` | Manages bound service account token signing keys |
| `AuditPolicyController` | Manages audit policy configuration |
| `TerminationObserver` | Tracks graceful termination metrics and late connection events |
| `WebhookSupportabilityController` | Validates webhook configurations and reports issues |
| `ServiceAccountIssuerController` | Syncs service account issuer configuration |
| `PodSecurityReadinessController` | Tracks pod security admission readiness |
| `HighCpuUsageAlertController` | Monitors and alerts on high API server CPU usage |
| `SCCReconcileController` | Reconciles SecurityContextConstraints |
| `LatencyProfileController` | Applies latency profile settings from node configuration |
| `NodeKubeconfigController` | Generates per-node kubeconfigs |
