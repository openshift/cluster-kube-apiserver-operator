# Pod Security Readiness Controller

The Pod Security Readiness Controller evaluates namespace compatibility with Pod Security Admission (PSA) enforcement in clusters.

## Purpose

This controller performs dry-run PSA evaluations to determine which namespaces would experience pod creation failures if PSA enforcement labels were applied.

The controller generates telemetry data for `ClusterFleetEvaluation` and helps us to understand PSA compatibility before enabling enforcement.

## Implementation

The controller follows this evaluation algorithm:

1. **Namespace Discovery** - Find namespaces without PSA enforcement
2. **PSA Level Determination** - Predict what enforcement level would be applied
3. **Dry-Run Evaluation** - Test namespace against predicted PSA level
4. **Violation Classification** - Categorize any violations found for telemetry

### Namespace Discovery

Selects namespaces without PSA enforcement labels:

```go
selector := "!pod-security.kubernetes.io/enforce"
```

### PSA Level Determination

The controller determines the effective PSA enforcement level using this precedence:

1. `security.openshift.io/MinimallySufficientPodSecurityStandard` annotation
2. Most restrictive of existing `pod-security.kubernetes.io/warn` or `pod-security.kubernetes.io/audit` labels, if owned by the PSA label syncer
3. Kube API server's future global default: `restricted`

### Dry-Run Evaluation

The controller performs the equivalent of this oc command:

```bash
oc label --dry-run=server --overwrite namespace $NAMESPACE_NAME \
    pod-security.kubernetes.io/enforce=$POD_SECURITY_STANDARD
```

PSA warnings during dry-run indicate the namespace contains violating workloads.

### Violation Classification

Violating namespaces are categorized for telemetry analysis:

| Classification   | Criteria                                                        | Purpose                                |
|------------------|-----------------------------------------------------------------|----------------------------------------|
| `runLevelZero`   | Core namespaces: `kube-system`, `default`, `kube-public`        | Platform infrastructure tracking       |
| `openshift`      | Namespaces with `openshift-` prefix                             | OpenShift component tracking           |
| `disabledSyncer` | Label `security.openshift.io/scc.podSecurityLabelSync: "false"` | Intentionally excluded namespaces      |
| `userSCC`        | Contains user workloads that violate PSA                        | SCC vs PSA policy conflicts            |
| `unknown`        | All other violating namespaces                                  | We simply don't know                   |
| `inconclusive`   | Evaluation failed due to API errors                             | Operational problems                   |

#### User SCC Detection

The PSA label syncer bases its evaluation exclusively on a ServiceAccount's SCCs, ignoring a user's SCCs.
When a pod's SCC assignment comes from user permissions rather than its ServiceAccount, the syncer's predicted PSA level may be incorrect.
Therefore we need to evaluate the affected pods (if any) against the target PSA level.

### Inconclusive Handling

When the evaluation process fails, namespaces are marked as `inconclusive`.

Common causes for inconclusive results:

- **API server unavailable** - Network timeouts, etcd issues
- **Resource conflicts** - Concurrent namespace modifications
- **Invalid PSA levels** - Malformed enforcement level strings
- **Pod listing failures** - RBAC issues or resource pressure

High rates of inconclusive results across the fleet may indicate systematic issues that requires investigation.

## Output

The controller updates `OperatorStatus` conditions for each violation type:

```go
type podSecurityOperatorConditions struct {
	violatingOpenShiftNamespaces      []string // PodSecurityOpenshiftEvaluationConditionsDetected
	violatingRunLevelZeroNamespaces   []string // PodSecurityRunLevelZeroEvaluationConditionsDetected
	violatingDisabledSyncerNamespaces []string // PodSecurityDisabledSyncerEvaluationConditionsDetected
	violatingUserSCCNamespaces        []string // PodSecurityUserSCCEvaluationConditionsDetected
	violatingUnclassifiedNamespaces   []string // PodSecurityUnknownEvaluationConditionsDetected
	inconclusiveNamespaces            []string // PodSecurityInconclusiveEvaluationConditionsDetected
}
```

Conditions follow the pattern:

- `PodSecurity{Type}EvaluationConditionsDetected`
- Status: `True` (violations found) / `False` (no violations)
- Message includes violating namespace list

## Configuration

The controller runs with a configurable interval (default: 4 hours) and uses rate limiting to avoid overwhelming the API server:

```go
kubeClientCopy.QPS = 2
kubeClientCopy.Burst = 2  
```

## Integration Points

- **PSA Label Syncer**: Reads syncer-managed PSA labels to predict enforcement levels
- **Cluster Operator**: Reports status through standard operator conditions
- **Telemetry**: Violation data feeds into cluster fleet analysis systems
