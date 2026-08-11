# TLS Security Profile Defaulter Job

## Summary

Add a CVO-managed Job at run level `0000_20_` that defaults `.spec.tlsSecurityProfile.type` to `Intermediate` on the `apiservers.config.openshift.io/cluster` resource during v4-to-v5 upgrades, before any operators process the config.

## Motivation

In OpenShift 5, the effective default TLS security profile is changing from `Intermediate` to `Modern`. For clusters upgrading from v4 with no explicit TLS security profile configured, this Job sets an explicit `Intermediate` default to preserve v4 behavior through the transition.

## Design

### Approach

A shell script delivered via ConfigMap, executed by a Kubernetes Job using the `cli` payload image (which provides `oc`). The Job runs at CVO run level `0000_20_`, within the same run level as the kube-apiserver operator but ordered before its Deployment, ensuring the default is set before any operators process the config.

The CVO treats Jobs as serialization barriers — the upgrade will not proceed past `0000_20_` until this Job completes successfully.

### Gating Logic

The script determines whether to act by inspecting `.status.history` on `ClusterVersion/version`:

1. Extract `history[0].version` (current/target version) and `history[1].version` (predecessor version).
2. If `history[1]` does not exist (fresh install, no predecessor) — exit 0.
3. Parse major versions from each (the portion before the first `.`).
4. If current major version is not `5` or predecessor major version is not `4` — exit 0.
5. Otherwise, proceed with defaulting.

### Defaulting Logic

1. Read `apiservers.config.openshift.io/cluster`.
2. If `.spec.tlsSecurityProfile` is null/missing, patch to set `.spec.tlsSecurityProfile.type` to `Intermediate`.
3. If `.spec.tlsSecurityProfile` exists but `.spec.tlsSecurityProfile.type` is empty string `""`, patch to set it to `Intermediate`.
4. Otherwise (type is already explicitly set) — exit 0, no action needed.

The apply uses `oc apply --server-side --field-manager=v4-to-v5-tls-security-profile-defaulter --force-conflicts`.

### Manifests

All manifests at run level `0000_20_` in `manifests/`. These apply only to self-managed clusters — in HyperShift, the APIServer config for hosted clusters is managed via the HostedCluster CR on the management cluster, not via the CVO payload.

All manifests are gated by the `TLSSecurityProfileModernDefault` feature gate. Since 4.23 and 5.0 are built from the same branch, this ensures the manifests are only included in payloads where the feature gate is enabled (5.0+), preventing the Job from running in 4.23 and becoming stale before a subsequent upgrade to 5.x.

```yaml
include.release.openshift.io/ibm-cloud-managed: "false"
include.release.openshift.io/self-managed-high-availability: "true"
include.release.openshift.io/single-node-developer: "true"
release.openshift.io/feature-gate: "TLSSecurityProfileModernDefault"
```

#### 1. ServiceAccount

- **File:** `0000_20_kube-apiserver-operator_05a_v4-to-v5-tls-defaulter-sa.yaml`
- **Name:** `v4-to-v5-tls-security-profile-defaulter`
- **Namespace:** `openshift-kube-apiserver-operator`

#### 2. NetworkPolicy

- **File:** `0000_20_kube-apiserver-operator_05b_v4-to-v5-tls-defaulter-networkpolicy.yaml`
- **Name:** `allow-v4-to-v5-tls-defaulter-egress`
- **Namespace:** `openshift-kube-apiserver-operator`
- **Purpose:** The namespace has a default-deny NetworkPolicy for both ingress and egress. The existing allow policy only covers pods with `app: kube-apiserver-operator`. This policy allows egress for the Job pod (matched via the `job-name` label automatically set by Kubernetes) so it can reach the API server.

#### 3. ClusterRole

- **File:** `0000_20_kube-apiserver-operator_05c_v4-to-v5-tls-defaulter-clusterrole.yaml`
- **Name:** `system:openshift:operator:v4-to-v5-tls-security-profile-defaulter`
- **Rules:**
  - `apiGroups: ["config.openshift.io"]`, `resources: ["apiservers"]`, `resourceNames: ["cluster"]`, `verbs: ["get", "patch"]`
  - `apiGroups: ["config.openshift.io"]`, `resources: ["clusterversions"]`, `resourceNames: ["version"]`, `verbs: ["get"]`

#### 4. ClusterRoleBinding

- **File:** `0000_20_kube-apiserver-operator_05d_v4-to-v5-tls-defaulter-clusterrolebinding.yaml`
- **Name:** `system:openshift:operator:v4-to-v5-tls-security-profile-defaulter`
- **RoleRef:** ClusterRole `system:openshift:operator:v4-to-v5-tls-security-profile-defaulter`
- **Subject:** ServiceAccount `v4-to-v5-tls-security-profile-defaulter` in `openshift-kube-apiserver-operator`

#### 5. ConfigMap

- **File:** `0000_20_kube-apiserver-operator_05e_v4-to-v5-tls-defaulter-script.yaml`
- **Name:** `v4-to-v5-tls-security-profile-defaulter-script`
- **Namespace:** `openshift-kube-apiserver-operator`
- **Data key:** `defaulter.sh` — the shell script implementing gating + defaulting logic

#### 6. Job

- **File:** `0000_20_kube-apiserver-operator_05f_v4-to-v5-tls-defaulter-job.yaml`
- **Name:** `v4-to-v5-tls-security-profile-defaulter`
- **Namespace:** `openshift-kube-apiserver-operator`
- **Image:** `cli` (referenced via `image-references`)
- **ServiceAccount:** `v4-to-v5-tls-security-profile-defaulter`
- **Script mount:** ConfigMap `v4-to-v5-tls-security-profile-defaulter-script` mounted and executed
- **Writable volume:** `emptyDir` mounted at `/tmp` with `HOME=/tmp` env var, required because `oc` caches API discovery information to `~/.kube/cache/discovery/` and the container uses `readOnlyRootFilesystem`
- **`activeDeadlineSeconds`:** 300 (5 minutes — generous for a simple oc patch)
- **`backoffLimit`:** 5
- **Pod security:** Compliant with `restricted` pod-security standard (runAsNonRoot, drop ALL capabilities, readOnlyRootFilesystem, seccompProfile RuntimeDefault)
- **Node placement:** Control plane nodes

#### 7. Image Reference

- Add `cli` tag to `manifests/image-references` pointing to `quay.io/openshift/origin-cli:v4.0`

### Script

```bash
#!/bin/bash
set -euo pipefail

# Get current and predecessor versions from ClusterVersion history
CURRENT_VERSION=$(oc get clusterversion version -o jsonpath='{.status.history[0].version}')
PREDECESSOR_VERSION=$(oc get clusterversion version -o jsonpath='{.status.history[1].version}')

# If no predecessor, this is a fresh install — nothing to do
if [[ -z "${PREDECESSOR_VERSION}" ]]; then
  echo "No predecessor version found (fresh install). Nothing to do."
  exit 0
fi

# Parse major versions
CURRENT_MAJOR="${CURRENT_VERSION%%.*}"
PREDECESSOR_MAJOR="${PREDECESSOR_VERSION%%.*}"

echo "Current version: ${CURRENT_VERSION} (major: ${CURRENT_MAJOR})"
echo "Predecessor version: ${PREDECESSOR_VERSION} (major: ${PREDECESSOR_MAJOR})"

# Only act on v4 -> v5 upgrades
if [[ "${CURRENT_MAJOR}" != "5" || "${PREDECESSOR_MAJOR}" != "4" ]]; then
  echo "Not a v4-to-v5 upgrade. Nothing to do."
  exit 0
fi

# Check current TLS security profile
TLS_TYPE=$(oc get apiserver cluster -o jsonpath='{.spec.tlsSecurityProfile.type}')

if [[ -z "${TLS_TYPE}" ]]; then
  echo "tlsSecurityProfile type is unset. Defaulting to Intermediate."
  oc apply --server-side --field-manager=v4-to-v5-tls-security-profile-defaulter --force-conflicts -f - <<PATCH
apiVersion: config.openshift.io/v1
kind: APIServer
metadata:
  name: cluster
spec:
  tlsSecurityProfile:
    type: Intermediate
PATCH
  echo "Successfully set tlsSecurityProfile.type to Intermediate."
else
  echo "tlsSecurityProfile.type is already set to '${TLS_TYPE}'. Nothing to do."
fi
```

### Idempotency

The script is safe to re-run:
- On non-upgrade scenarios: exits early due to version checks.
- On re-syncs after successful run: tlsSecurityProfile.type is already set, so no patch occurs.
- On fresh v5 installs: no predecessor version, exits early.

### Error Handling

- The script uses `set -euo pipefail` and fails fast on any error.
- Retries are handled at the Job level via `backoffLimit: 5` with `activeDeadlineSeconds: 300`. Kubernetes exponential backoff (10s, 20s, 40s, 80s, 160s) allows up to 5 attempts within the 5-minute window.
- All "nothing to do" paths exit 0 immediately.

### HyperShift

This Job only applies to self-managed clusters. In HyperShift, the APIServer configuration for hosted clusters is managed via the `HostedCluster` CR on the management cluster, not via the CVO payload on the hosted cluster.

Equivalent defaulting for HyperShift hosted clusters upgrading from v4 to v5 should be implemented in the HyperShift operator, which already manages the HostedCluster lifecycle and can detect upgrade transitions. This is a separate work item outside the scope of this repository.
