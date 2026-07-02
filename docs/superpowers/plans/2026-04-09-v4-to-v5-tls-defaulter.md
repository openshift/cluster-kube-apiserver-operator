# v4-to-v5 TLS Security Profile Defaulter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CVO-managed manifests at run level `0000_20_` that default `tlsSecurityProfile.type` to `Intermediate` on `apiservers.config.openshift.io/cluster` during v4-to-v5 upgrades.

**Architecture:** A shell script in a ConfigMap, executed by a Job using the `cli` payload image. RBAC is scoped to only the resources the script needs. A NetworkPolicy allows egress for the Job pod (the namespace has default-deny). All manifests are excluded from HyperShift (`ibm-cloud-managed: "false"`) and gated by the `TLSSecurityProfileModernDefault` feature gate to ensure they only appear in 5.0+ payloads (since 4.23 and 5.0 build from the same branch).

**Tech Stack:** Kubernetes YAML manifests, bash, `oc` CLI

**Spec:** `docs/superpowers/specs/2026-04-09-tls-security-profile-defaulter-design.md`

---

## File Map

All files are in `manifests/`:

| File | Purpose |
|------|---------|
| `0000_20_kube-apiserver-operator_05a_v4-to-v5-tls-defaulter-sa.yaml` | ServiceAccount for the Job |
| `0000_20_kube-apiserver-operator_05b_v4-to-v5-tls-defaulter-networkpolicy.yaml` | NetworkPolicy allowing egress for the Job pod |
| `0000_20_kube-apiserver-operator_05c_v4-to-v5-tls-defaulter-clusterrole.yaml` | ClusterRole with minimal RBAC |
| `0000_20_kube-apiserver-operator_05d_v4-to-v5-tls-defaulter-clusterrolebinding.yaml` | Binds ClusterRole to ServiceAccount |
| `0000_20_kube-apiserver-operator_05e_v4-to-v5-tls-defaulter-script.yaml` | ConfigMap containing the defaulter shell script |
| `0000_20_kube-apiserver-operator_05f_v4-to-v5-tls-defaulter-job.yaml` | Job that runs the script |
| `image-references` | Add `cli` image tag (modify existing file) |

---

### Task 1: ServiceAccount

**Files:**
- Create: `manifests/0000_05_kube-apiserver-operator_00_v4-to-v5-tls-defaulter-sa.yaml`

- [ ] **Step 1: Create the ServiceAccount manifest**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  namespace: openshift-kube-apiserver-operator
  name: v4-to-v5-tls-security-profile-defaulter
  annotations:
    include.release.openshift.io/ibm-cloud-managed: "false"
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_00_v4-to-v5-tls-defaulter-sa.yaml'))"`
Expected: No output (success)

- [ ] **Step 3: Commit**

```bash
git add manifests/0000_05_kube-apiserver-operator_00_v4-to-v5-tls-defaulter-sa.yaml
git commit -m "Add ServiceAccount for v4-to-v5 TLS security profile defaulter Job"
```

---

### Task 2: ClusterRole

**Files:**
- Create: `manifests/0000_05_kube-apiserver-operator_01_v4-to-v5-tls-defaulter-clusterrole.yaml`

- [ ] **Step 1: Create the ClusterRole manifest**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: system:openshift:operator:v4-to-v5-tls-security-profile-defaulter
  annotations:
    include.release.openshift.io/ibm-cloud-managed: "false"
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
rules:
- apiGroups:
  - config.openshift.io
  resources:
  - apiservers
  resourceNames:
  - cluster
  verbs:
  - get
  - patch
- apiGroups:
  - config.openshift.io
  resources:
  - clusterversions
  resourceNames:
  - version
  verbs:
  - get
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_01_v4-to-v5-tls-defaulter-clusterrole.yaml'))"`
Expected: No output (success)

- [ ] **Step 3: Commit**

```bash
git add manifests/0000_05_kube-apiserver-operator_01_v4-to-v5-tls-defaulter-clusterrole.yaml
git commit -m "Add ClusterRole for v4-to-v5 TLS security profile defaulter Job"
```

---

### Task 3: ClusterRoleBinding

**Files:**
- Create: `manifests/0000_05_kube-apiserver-operator_02_v4-to-v5-tls-defaulter-clusterrolebinding.yaml`

- [ ] **Step 1: Create the ClusterRoleBinding manifest**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:operator:v4-to-v5-tls-security-profile-defaulter
  annotations:
    include.release.openshift.io/ibm-cloud-managed: "false"
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:operator:v4-to-v5-tls-security-profile-defaulter
subjects:
- kind: ServiceAccount
  namespace: openshift-kube-apiserver-operator
  name: v4-to-v5-tls-security-profile-defaulter
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_02_v4-to-v5-tls-defaulter-clusterrolebinding.yaml'))"`
Expected: No output (success)

- [ ] **Step 3: Commit**

```bash
git add manifests/0000_05_kube-apiserver-operator_02_v4-to-v5-tls-defaulter-clusterrolebinding.yaml
git commit -m "Add ClusterRoleBinding for v4-to-v5 TLS security profile defaulter Job"
```

---

### Task 4: ConfigMap with defaulter script

**Files:**
- Create: `manifests/0000_05_kube-apiserver-operator_03_v4-to-v5-tls-defaulter-script.yaml`

- [ ] **Step 1: Create the ConfigMap manifest**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver-operator
  name: v4-to-v5-tls-security-profile-defaulter-script
  annotations:
    include.release.openshift.io/ibm-cloud-managed: "false"
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
data:
  defaulter.sh: |
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

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_03_v4-to-v5-tls-defaulter-script.yaml'))"`
Expected: No output (success)

- [ ] **Step 3: Verify the embedded script parses correctly**

Run: `python3 -c "import yaml; cm = yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_03_v4-to-v5-tls-defaulter-script.yaml')); print(cm['data']['defaulter.sh'])"`
Expected: The script is printed with correct indentation and no YAML artifacts

- [ ] **Step 4: Commit**

```bash
git add manifests/0000_05_kube-apiserver-operator_03_v4-to-v5-tls-defaulter-script.yaml
git commit -m "Add ConfigMap with defaulter script for v4-to-v5 TLS security profile Job"
```

---

### Task 5: Job

**Files:**
- Create: `manifests/0000_05_kube-apiserver-operator_04_v4-to-v5-tls-defaulter-job.yaml`

- [ ] **Step 1: Create the Job manifest**

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  namespace: openshift-kube-apiserver-operator
  name: v4-to-v5-tls-security-profile-defaulter
  annotations:
    include.release.openshift.io/ibm-cloud-managed: "false"
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
spec:
  activeDeadlineSeconds: 300
  backoffLimit: 5
  template:
    metadata:
      name: v4-to-v5-tls-security-profile-defaulter
      annotations:
        target.workload.openshift.io/management: '{"effect": "PreferredDuringScheduling"}'
    spec:
      serviceAccountName: v4-to-v5-tls-security-profile-defaulter
      restartPolicy: OnFailure
      hostUsers: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
      - name: defaulter
        image: quay.io/openshift/origin-cli:v4.0
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: ["ALL"]
        command:
        - /bin/bash
        - /scripts/defaulter.sh
        resources:
          requests:
            memory: 50Mi
            cpu: 10m
        volumeMounts:
        - name: scripts
          mountPath: /scripts
          readOnly: true
        terminationMessagePolicy: FallbackToLogsOnError
      volumes:
      - name: scripts
        configMap:
          name: v4-to-v5-tls-security-profile-defaulter-script
      nodeSelector:
        node-role.kubernetes.io/master: ""
      priorityClassName: "system-cluster-critical"
      tolerations:
      - key: "node-role.kubernetes.io/master"
        operator: "Exists"
        effect: "NoSchedule"
      - key: "node-role.kubernetes.io/control-plane"
        operator: "Exists"
        effect: "NoExecute"
      - key: "node.kubernetes.io/unreachable"
        operator: "Exists"
        effect: "NoExecute"
        tolerationSeconds: 120
      - key: "node.kubernetes.io/not-ready"
        operator: "Exists"
        effect: "NoExecute"
        tolerationSeconds: 120
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/0000_05_kube-apiserver-operator_04_v4-to-v5-tls-defaulter-job.yaml'))"`
Expected: No output (success)

- [ ] **Step 3: Commit**

```bash
git add manifests/0000_05_kube-apiserver-operator_04_v4-to-v5-tls-defaulter-job.yaml
git commit -m "Add Job for v4-to-v5 TLS security profile defaulter"
```

---

### Task 6: Add cli image reference

**Files:**
- Modify: `manifests/image-references`

- [ ] **Step 1: Add the cli image tag**

Add the following entry to `spec.tags` in `manifests/image-references`:

```yaml
  - name: cli
    from:
      kind: DockerImage
      name: quay.io/openshift/origin-cli:v4.0
```

The full file should be:

```yaml
kind: ImageStream
apiVersion: image.openshift.io/v1
spec:
  tags:
  - name: cluster-kube-apiserver-operator
    from:
      kind: DockerImage
      name: docker.io/openshift/origin-cluster-kube-apiserver-operator:v4.0
  - name: hyperkube
    from:
      kind: DockerImage
      name: quay.io/openshift/origin-hyperkube:v4.0
  - name: cli
    from:
      kind: DockerImage
      name: quay.io/openshift/origin-cli:v4.0
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('manifests/image-references'))"`
Expected: No output (success)

- [ ] **Step 3: Commit**

```bash
git add manifests/image-references
git commit -m "Add cli image reference for v4-to-v5 TLS security profile defaulter Job"
```

---

### Task 7: Verify all manifests

- [ ] **Step 1: Verify all new files exist**

Run: `ls -1 manifests/0000_05_kube-apiserver-operator_*`
Expected:
```
manifests/0000_05_kube-apiserver-operator_00_v4-to-v5-tls-defaulter-sa.yaml
manifests/0000_05_kube-apiserver-operator_01_v4-to-v5-tls-defaulter-clusterrole.yaml
manifests/0000_05_kube-apiserver-operator_02_v4-to-v5-tls-defaulter-clusterrolebinding.yaml
manifests/0000_05_kube-apiserver-operator_03_v4-to-v5-tls-defaulter-script.yaml
manifests/0000_05_kube-apiserver-operator_04_v4-to-v5-tls-defaulter-job.yaml
```

- [ ] **Step 2: Validate all YAML files parse cleanly**

Run: `for f in manifests/0000_05_kube-apiserver-operator_*; do python3 -c "import yaml; yaml.safe_load(open('$f'))" && echo "OK: $f" || echo "FAIL: $f"; done`
Expected: All files show `OK`

- [ ] **Step 3: Verify cross-references are consistent**

Check these match across manifests:
- ServiceAccount name `v4-to-v5-tls-security-profile-defaulter` in SA, ClusterRoleBinding subjects, and Job `serviceAccountName`
- ClusterRole name `system:openshift:operator:v4-to-v5-tls-security-profile-defaulter` in ClusterRole and ClusterRoleBinding roleRef
- ConfigMap name `v4-to-v5-tls-security-profile-defaulter-script` in ConfigMap and Job volume
- All files have `include.release.openshift.io/ibm-cloud-managed: "false"`

Run: `grep -h 'v4-to-v5-tls-security-profile-defaulter' manifests/0000_05_kube-apiserver-operator_* | sort`
Expected: All references use the same names consistently

- [ ] **Step 4: Run any existing repo validation**

Run: `make verify 2>&1 | tail -20` (if a verify target exists)
Expected: No failures related to the new manifests
