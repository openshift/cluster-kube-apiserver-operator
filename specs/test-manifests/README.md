# Manual Testing for PSA User SCC Violation Detection

This directory contains test manifests and verification commands to manually test the PSA user SCC violation detection feature.

## Test Scenarios

### Scenario 1: User-created Pod with Privileged SCC

1. Create a test namespace.
2. Verify that the namespace has the restricted pod-security.kubernetes.io/audit and pod-security.kubernetes.io/warn labels.
3. Add Pod manifest that requires privileged SCC.
4. Verify with `kubectl label --dry-run=server --overwrite namespace --all pod-security.kubernetes.io/enforce=restricted` returns warnings.
5. Wait for ClusterFleetEvaluation, as createad by podsecurityreadinesscontroller.
6. There should be a notion of `PodSecurityUserSCCViolationConditionsDetected` in the status.
