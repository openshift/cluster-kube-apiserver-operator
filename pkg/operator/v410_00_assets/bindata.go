// Code generated for package v410_00_assets by go-bindata DO NOT EDIT. (@generated)
// sources:
// bindata/v4.1.0/alerts/api-usage.yaml
// bindata/v4.1.0/alerts/cpu-utilization.yaml
// bindata/v4.1.0/alerts/kube-apiserver-requests.yaml
// bindata/v4.1.0/alerts/kube-apiserver-slos.yaml
// bindata/v4.1.0/config/config-overrides.yaml
// bindata/v4.1.0/config/defaultconfig.yaml
// bindata/v4.1.0/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrole-crd-reader.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrole-node-reader.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrole.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-auth-delegator.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-crd-reader.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-node-reader.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-kubeconfig-cm.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-rolebinding-kube-system.yaml
// bindata/v4.1.0/kube-apiserver/check-endpoints-rolebinding.yaml
// bindata/v4.1.0/kube-apiserver/cm.yaml
// bindata/v4.1.0/kube-apiserver/control-plane-node-kubeconfig-cm.yaml
// bindata/v4.1.0/kube-apiserver/delegated-incluster-authentication-rolebinding.yaml
// bindata/v4.1.0/kube-apiserver/kubeconfig-cm.yaml
// bindata/v4.1.0/kube-apiserver/localhost-recovery-client-crb.yaml
// bindata/v4.1.0/kube-apiserver/localhost-recovery-sa.yaml
// bindata/v4.1.0/kube-apiserver/localhost-recovery-token.yaml
// bindata/v4.1.0/kube-apiserver/node-kubeconfigs.yaml
// bindata/v4.1.0/kube-apiserver/ns.yaml
// bindata/v4.1.0/kube-apiserver/pod-cm.yaml
// bindata/v4.1.0/kube-apiserver/pod.yaml
// bindata/v4.1.0/kube-apiserver/recovery-config.yaml
// bindata/v4.1.0/kube-apiserver/recovery-encryption-config.yaml
// bindata/v4.1.0/kube-apiserver/recovery-pod.yaml
// bindata/v4.1.0/kube-apiserver/rollout-monitor-pod-cm.yaml
// bindata/v4.1.0/kube-apiserver/rollout-monitor-pod.yaml
// bindata/v4.1.0/kube-apiserver/storage-version-migration-flowschema.yaml
// bindata/v4.1.0/kube-apiserver/storage-version-migration-prioritylevelconfiguration.yaml
// bindata/v4.1.0/kube-apiserver/svc.yaml
// bindata/v4.1.0/kube-apiserver/trusted-ca-cm.yaml
package v410_00_assets

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type asset struct {
	bytes []byte
	info  os.FileInfo
}

type bindataFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Name return file name
func (fi bindataFileInfo) Name() string {
	return fi.name
}

// Size return file size
func (fi bindataFileInfo) Size() int64 {
	return fi.size
}

// Mode return file mode
func (fi bindataFileInfo) Mode() os.FileMode {
	return fi.mode
}

// Mode return file modify time
func (fi bindataFileInfo) ModTime() time.Time {
	return fi.modTime
}

// IsDir return file whether a directory
func (fi bindataFileInfo) IsDir() bool {
	return fi.mode&os.ModeDir != 0
}

// Sys return file is sys mode
func (fi bindataFileInfo) Sys() interface{} {
	return nil
}

var _v410AlertsApiUsageYaml = []byte(`apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: api-usage
  namespace: openshift-kube-apiserver
spec:
  groups:
    - name: pre-release-lifecycle
      rules:
        - alert: APIRemovedInNextReleaseInUse
          annotations:
            message: >-
              Deprecated API that will be removed in the next version is being used. Removing the workload that is using
              the {{ $labels.group }}.{{ $labels.version }}/{{ $labels.resource }} API might be necessary for
              a successful upgrade to the next cluster version.
              Refer to the apirequestcount.apiserver.openshift.io resources to identify the workload.
          expr: |
            group(apiserver_requested_deprecated_apis{removed_release="1.22"}) by (group,version,resource) and (sum by(group,version,resource) (rate(apiserver_request_total{system_client!="kube-controller-manager",system_client!="cluster-policy-controller"}[4h]))) > 0
          for: 1h
          labels:
            severity: info
        - alert: APIRemovedInNextEUSReleaseInUse
          annotations:
            message: >-
              Deprecated API that will be removed in the next EUS version is being used. Removing the workload that is using
              the {{ $labels.group }}.{{ $labels.version }}/{{ $labels.resource }} API might be necessary for
              a successful upgrade to the next EUS cluster version.
              Refer to the apirequestcount.apiserver.openshift.io resources to identify the workload.
          expr: |
            group(apiserver_requested_deprecated_apis{removed_release=~"1\\.2[123]"}) by (group,version,resource) and (sum by(group,version,resource) (rate(apiserver_request_total{system_client!="kube-controller-manager",system_client!="cluster-policy-controller"}[4h]))) > 0

          for: 1h
          labels:
            severity: info
`)

func v410AlertsApiUsageYamlBytes() ([]byte, error) {
	return _v410AlertsApiUsageYaml, nil
}

func v410AlertsApiUsageYaml() (*asset, error) {
	bytes, err := v410AlertsApiUsageYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/alerts/api-usage.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410AlertsCpuUtilizationYaml = []byte(`apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: cpu-utilization
  namespace: openshift-kube-apiserver
spec:
  groups:
    - name: control-plane-cpu-utilization
      rules:
        - alert: HighOverallControlPlaneCPU
          annotations:
            summary: >-
              CPU utilization across all three control plane nodes is higher than two control plane nodes can sustain; a single control plane node outage may
              cause a cascading failure; increase available CPU.
            message: >-
              Given three control plane nodes, the overall CPU utilization may only be about 2/3 of all available capacity.
              This is because if a single control plane node fails, the remaining two must handle the load of the cluster in order to be HA.
              If the cluster is using more than 2/3 of all capacity, if one control plane node fails, the remaining two are likely to
              fail when they take the load.
              To fix this, increase the CPU and memory on your control plane nodes.
          expr: |
            sum(
              100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)
              AND on (instance) label_replace( kube_node_role{role="master"}, "instance", "$1", "node", "(.+)" )
            )
            /
            count(kube_node_role{role="master"})
            > 60
          for: 10m
          labels:
            severity: warning
        - alert: ExtremelyHighIndividualControlPlaneCPU
          annotations:
            summary: >-
              CPU utilization on a single control plane node is very high, more CPU pressure is likely to cause a failover; increase available CPU.
            message: >-
              Extreme CPU pressure can cause slow serialization and poor performance from the kube-apiserver and etcd.
              When this happens, there is a risk of clients seeing non-responsive API requests which are issued again
              causing even more CPU pressure.
              It can also cause failing liveness probes due to slow etcd responsiveness on the backend.
              If one kube-apiserver fails under this condition, chances are you will experience a cascade as the remaining
              kube-apiservers are also under-provisioned.
              To fix this, increase the CPU and memory on your control plane nodes.
          expr: |
            100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100) > 90 AND on (instance) label_replace( kube_node_role{role="master"}, "instance", "$1", "node", "(.+)" )
          for: 5m
          labels:
            severity: critical
`)

func v410AlertsCpuUtilizationYamlBytes() ([]byte, error) {
	return _v410AlertsCpuUtilizationYaml, nil
}

func v410AlertsCpuUtilizationYaml() (*asset, error) {
	bytes, err := v410AlertsCpuUtilizationYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/alerts/cpu-utilization.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410AlertsKubeApiserverRequestsYaml = []byte(`apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kube-apiserver-requests
  namespace: openshift-kube-apiserver
spec:
  groups:
    - name: apiserver-requests-in-flight
      rules:
        # We want to capture requests in-flight metrics for kube-apiserver and openshift-apiserver.
        # apiserver='kube-apiserver' indicates that the source is kubernetes apiserver.
        # apiserver='openshift-apiserver' indicates that the source is openshift apiserver.
        # The subquery aggregates by apiserver and request kind. requestKind is {mutating|readOnly}
        # The following query gives us maximum peak of the apiserver concurrency over a 2-minute window.
        - record: cluster:apiserver_current_inflight_requests:sum:max_over_time:2m
          expr: |
            max_over_time(sum(apiserver_current_inflight_requests{apiserver=~"openshift-apiserver|kube-apiserver"}) by (apiserver,requestKind)[2m:])
`)

func v410AlertsKubeApiserverRequestsYamlBytes() ([]byte, error) {
	return _v410AlertsKubeApiserverRequestsYaml, nil
}

func v410AlertsKubeApiserverRequestsYaml() (*asset, error) {
	bytes, err := v410AlertsKubeApiserverRequestsYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/alerts/kube-apiserver-requests.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410AlertsKubeApiserverSlosYaml = []byte(`apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kube-apiserver-slos
  namespace: openshift-kube-apiserver
spec:
  groups:
  - name: kube-apiserver-slos
    rules:
    - alert: KubeAPIErrorBudgetBurn
      annotations:
        description: The API server is burning too much error budget. This alert fires when too many requests are failing with high latency. Use the 'API Performance' monitoring dashboards to narrow down the request states and latency. The 'etcd' monitoring dashboards also provides metrics to help determine etcd stability and performance.
        summary: The API server is burning too much error budget.
      expr: |
        sum(apiserver_request:burnrate1h) > (14.40 * 0.01000)
        and
        sum(apiserver_request:burnrate5m) > (14.40 * 0.01000)
      for: 2m
      labels:
        long: 1h
        severity: critical
        short: 5m
    - alert: KubeAPIErrorBudgetBurn
      annotations:
        description: The API server is burning too much error budget. This alert fires when too many requests are failing with high latency. Use the 'API Performance' monitoring dashboards to narrow down the request states and latency. The 'etcd' monitoring dashboards also provides metrics to help determine etcd stability and performance.
        summary: The API server is burning too much error budget.
      expr: |
        sum(apiserver_request:burnrate6h) > (6.00 * 0.01000)
        and
        sum(apiserver_request:burnrate30m) > (6.00 * 0.01000)
      for: 15m
      labels:
        long: 6h
        severity: critical
        short: 30m
    - alert: KubeAPIErrorBudgetBurn
      annotations:
        description: The API server is burning too much error budget. This alert fires when too many requests are failing with high latency. Use the 'API Performance' monitoring dashboards to narrow down the request states and latency. The 'etcd' monitoring dashboards also provides metrics to help determine etcd stability and performance.
        summary: The API server is burning too much error budget.
      expr: |
        sum(apiserver_request:burnrate1d) > (3.00 * 0.01000)
        and
        sum(apiserver_request:burnrate2h) > (3.00 * 0.01000)
      for: 1h
      labels:
        long: 1d
        severity: warning
        short: 2h
    - alert: KubeAPIErrorBudgetBurn
      annotations:
        description: The API server is burning too much error budget. This alert fires when too many requests are failing with high latency. Use the 'API Performance' monitoring dashboards to narrow down the request states and latency. The 'etcd' monitoring dashboards also provides metrics to help determine etcd stability and performance.
        summary: The API server is burning too much error budget.
      expr: |
        sum(apiserver_request:burnrate3d) > (1.00 * 0.01000)
        and
        sum(apiserver_request:burnrate6h) > (1.00 * 0.01000)
      for: 3h
      labels:
        long: 3d
        severity: warning
        short: 6h
  - name: kube-apiserver.rules
    rules:
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[5m]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[5m])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[5m]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[5m])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[5m])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[5m]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[5m]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[5m])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[5m]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[5m]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[5m])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate5m
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[30m]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[30m])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[30m]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[30m])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[30m])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[30m]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[30m]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[30m])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[30m]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[30m]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[30m])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate30m
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[1h]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[1h])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[1h]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[1h])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[1h])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[1h]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[1h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[1h])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[1h]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[1h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[1h])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate1h
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[2h]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[2h])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[2h]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[2h])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[2h])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[2h]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[2h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[2h])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[2h]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[2h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[2h])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate2h
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[6h]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[6h])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[6h]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[6h])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[6h])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[6h]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[6h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[6h])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[6h]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[6h]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[6h])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate6h
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[1d]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[1d])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[1d]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[1d])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[1d])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[1d]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[1d]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[1d])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[1d]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[1d]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[1d])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate1d
    - expr: |
        # error
        label_replace(
          sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",code=~"5.."}[3d]))
        / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[3d])))
        , "type", "error", "_none_", "")
        or
        # resource-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource"}[3d]))
          -
            (sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="resource",le="0.1"}[3d])) or vector(0))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[3d])))
        , "type", "slow-resource", "_none_", "")
        or
        # namespace-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace"}[3d]))
          - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec",scope="namespace",le="0.5"}[3d]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET",subresource!~"proxy|log|exec"}[3d])))
        , "type", "slow-namespace", "_none_", "")
        or
        # cluster-scoped latency
        label_replace(
          (
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"LIST|GET",scope="cluster"}[3d]))
            - sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET",scope="cluster",le="5"}[3d]))
          ) / scalar(sum(rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[3d])))
        , "type", "slow-cluster", "_none_", "")
      labels:
        verb: read
      record: apiserver_request:burnrate3d
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[1d]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[1d]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[1d]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[1d]))
      labels:
        verb: write
      record: apiserver_request:burnrate1d
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[1h]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[1h]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[1h]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[1h]))
      labels:
        verb: write
      record: apiserver_request:burnrate1h
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[2h]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[2h]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[2h]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[2h]))
      labels:
        verb: write
      record: apiserver_request:burnrate2h
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[30m]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[30m]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[30m]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[30m]))
      labels:
        verb: write
      record: apiserver_request:burnrate30m
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[3d]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[3d]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[3d]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[3d]))
      labels:
        verb: write
      record: apiserver_request:burnrate3d
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[5m]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[5m]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[5m]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[5m]))
      labels:
        verb: write
      record: apiserver_request:burnrate5m
    - expr: |
        (
          (
            # too slow
            sum(rate(apiserver_request_duration_seconds_count{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[6h]))
            -
            sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",le="1"}[6h]))
          )
          +
          sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE",code=~"5.."}[6h]))
        )
        /
        sum(rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[6h]))
      labels:
        verb: write
      record: apiserver_request:burnrate6h
    - expr: |
        sum by (code,resource) (rate(apiserver_request_total{job="apiserver",verb=~"LIST|GET"}[5m]))
      labels:
        verb: read
      record: code_resource:apiserver_request_total:rate5m
    - expr: |
        sum by (code,resource) (rate(apiserver_request_total{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[5m]))
      labels:
        verb: write
      record: code_resource:apiserver_request_total:rate5m
    - expr: |
        histogram_quantile(0.99, sum by (le, resource) (rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"LIST|GET"}[5m]))) > 0
      labels:
        quantile: "0.99"
        verb: read
      record: cluster_quantile:apiserver_request_duration_seconds:histogram_quantile
    - expr: |
        histogram_quantile(0.99, sum by (le, resource) (rate(apiserver_request_duration_seconds_bucket{job="apiserver",verb=~"POST|PUT|PATCH|DELETE"}[5m]))) > 0
      labels:
        quantile: "0.99"
        verb: write
      record: cluster_quantile:apiserver_request_duration_seconds:histogram_quantile
    - expr: |
        histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",subresource!="log",verb!~"LIST|WATCH|WATCHLIST|DELETECOLLECTION|PROXY|CONNECT"}[5m])) without(instance, pod))
      labels:
        quantile: "0.99"
      record: cluster_quantile:apiserver_request_duration_seconds:histogram_quantile
    - expr: |
        histogram_quantile(0.9, sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",subresource!="log",verb!~"LIST|WATCH|WATCHLIST|DELETECOLLECTION|PROXY|CONNECT"}[5m])) without(instance, pod))
      labels:
        quantile: "0.9"
      record: cluster_quantile:apiserver_request_duration_seconds:histogram_quantile
    - expr: |
        histogram_quantile(0.5, sum(rate(apiserver_request_duration_seconds_bucket{job="apiserver",subresource!="log",verb!~"LIST|WATCH|WATCHLIST|DELETECOLLECTION|PROXY|CONNECT"}[5m])) without(instance, pod))
      labels:
        quantile: "0.5"
      record: cluster_quantile:apiserver_request_duration_seconds:histogram_quantile
`)

func v410AlertsKubeApiserverSlosYamlBytes() ([]byte, error) {
	return _v410AlertsKubeApiserverSlosYaml, nil
}

func v410AlertsKubeApiserverSlosYaml() (*asset, error) {
	bytes, err := v410AlertsKubeApiserverSlosYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/alerts/kube-apiserver-slos.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410ConfigConfigOverridesYaml = []byte(`apiVersion: kubecontrolplane.config.openshift.io/v1
kind: KubeAPIServerConfig
apiServerArguments:
  # The following arguments are required to enable bound sa
  # tokens. This is only supported post-bootstrap so these
  # values must not appear in defaultconfig.yaml.
  service-account-issuer:
    - https://kubernetes.default.svc
  api-audiences:
    - https://kubernetes.default.svc
  service-account-signing-key-file:
    - /etc/kubernetes/static-pod-certs/secrets/bound-service-account-signing-key/service-account.key
serviceAccountPublicKeyFiles:
  # this being a directory means we cannot directly use the upstream flags.
  # TODO make a configobserver that writes the individual values that we need.
  - /etc/kubernetes/static-pod-resources/configmaps/sa-token-signing-certs
  # The following path contains the public keys needed to verify bound sa
  # tokens. This is only supported post-bootstrap.
  - /etc/kubernetes/static-pod-resources/configmaps/bound-sa-token-signing-certs

`)

func v410ConfigConfigOverridesYamlBytes() ([]byte, error) {
	return _v410ConfigConfigOverridesYaml, nil
}

func v410ConfigConfigOverridesYaml() (*asset, error) {
	bytes, err := v410ConfigConfigOverridesYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/config/config-overrides.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410ConfigDefaultconfigYaml = []byte(`apiVersion: kubecontrolplane.config.openshift.io/v1
kind: KubeAPIServerConfig
admission:
  pluginConfig:
    network.openshift.io/ExternalIPRanger:
      configuration:
        allowIngressIP: true
        apiVersion: network.openshift.io/v1
        externalIPNetworkCIDRs: null
        kind: ExternalIPRangerAdmissionConfig
      location: ""
apiServerArguments:
  allow-privileged:
    - "true"
  anonymous-auth:
    - "true"
  authorization-mode:
    - Scope
    - SystemMasters
    - RBAC
    - Node
  audit-log-format:
    - json
  audit-log-maxbackup:
    - "10"
  audit-log-maxsize:
    - "100"
  audit-log-path:
    - /var/log/kube-apiserver/audit.log
  audit-policy-file:
    - /etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-audit-policies/default.yaml
  client-ca-file:
    - /etc/kubernetes/static-pod-certs/configmaps/client-ca/ca-bundle.crt
  enable-admission-plugins:
    - CertificateApproval
    - CertificateSigning
    - CertificateSubjectRestriction
    - DefaultIngressClass
    - DefaultStorageClass
    - DefaultTolerationSeconds
    - LimitRanger
    - MutatingAdmissionWebhook
    - NamespaceLifecycle
    - NodeRestriction
    - OwnerReferencesPermissionEnforcement
    - PersistentVolumeClaimResize
    - PersistentVolumeLabel
    - PodNodeSelector
    - PodTolerationRestriction
    - Priority
    - ResourceQuota
    - RuntimeClass
    - ServiceAccount
    - StorageObjectInUseProtection
    - TaintNodesByCondition
    - ValidatingAdmissionWebhook
    - authorization.openshift.io/RestrictSubjectBindings
    - authorization.openshift.io/ValidateRoleBindingRestriction
    - config.openshift.io/DenyDeleteClusterConfiguration
    - config.openshift.io/ValidateAPIServer
    - config.openshift.io/ValidateAuthentication
    - config.openshift.io/ValidateConsole
    - config.openshift.io/ValidateFeatureGate
    - config.openshift.io/ValidateImage
    - config.openshift.io/ValidateOAuth
    - config.openshift.io/ValidateProject
    - config.openshift.io/ValidateScheduler
    - image.openshift.io/ImagePolicy
    - network.openshift.io/ExternalIPRanger
    - network.openshift.io/RestrictedEndpointsAdmission
    - quota.openshift.io/ClusterResourceQuota
    - quota.openshift.io/ValidateClusterResourceQuota
    - route.openshift.io/IngressAdmission
    - scheduling.openshift.io/OriginPodNodeEnvironment
    - security.openshift.io/DefaultSecurityContextConstraints
    - security.openshift.io/SCCExecRestrictions
    - security.openshift.io/SecurityContextConstraint
    - security.openshift.io/ValidateSecurityContextConstraints
  # switch to direct pod IP routing for aggregated apiservers to avoid service IPs as on source of instability
  enable-aggregator-routing:
    - "true"
  enable-logs-handler:
    - "false"
  enable-swagger-ui:
    - "true"
  endpoint-reconciler-type:
    - "lease"
  etcd-cafile:
    - /etc/kubernetes/static-pod-resources/configmaps/etcd-serving-ca/ca-bundle.crt
  etcd-certfile:
    - /etc/kubernetes/static-pod-resources/secrets/etcd-client/tls.crt
  etcd-keyfile:
    - /etc/kubernetes/static-pod-resources/secrets/etcd-client/tls.key
  etcd-prefix:
    - kubernetes.io
  event-ttl:
    - 3h
  goaway-chance:
    - "0"
  http2-max-streams-per-connection:
    - "2000"  # recommended is 1000, but we need to mitigate https://github.com/kubernetes/kubernetes/issues/74412
  insecure-port:
    - "0"
  kubelet-certificate-authority:
    - /etc/kubernetes/static-pod-resources/configmaps/kubelet-serving-ca/ca-bundle.crt
  kubelet-client-certificate:
    - /etc/kubernetes/static-pod-resources/secrets/kubelet-client/tls.crt
  kubelet-client-key:
    - /etc/kubernetes/static-pod-resources/secrets/kubelet-client/tls.key
  kubelet-https:
    - "true"
  kubelet-preferred-address-types:
    - InternalIP # all of our kubelets have internal IPs and we *only* support communicating with them via that internal IP so that NO_PROXY always works and is lightweight
  kubelet-read-only-port:
    - "0"
  kubernetes-service-node-port:
    - "0"
  # value should logically scale with max-requests-inflight
  max-mutating-requests-inflight:
    - "1000"
  # value needed to be bumped for scale tests.  The kube-apiserver did ok here
  max-requests-inflight:
    - "3000"
  min-request-timeout:
    - "3600"
  proxy-client-cert-file:
    - /etc/kubernetes/static-pod-certs/secrets/aggregator-client/tls.crt
  proxy-client-key-file:
    - /etc/kubernetes/static-pod-certs/secrets/aggregator-client/tls.key
  requestheader-allowed-names:
    - kube-apiserver-proxy
    - system:kube-apiserver-proxy
    - system:openshift-aggregator
  requestheader-client-ca-file:
    - /etc/kubernetes/static-pod-certs/configmaps/aggregator-client-ca/ca-bundle.crt
  requestheader-extra-headers-prefix:
    - X-Remote-Extra-
  requestheader-group-headers:
    - X-Remote-Group
  requestheader-username-headers:
    - X-Remote-User
  # need to enable alpha APIs for the priority and fairness feature
  service-account-lookup:
    - "true"
  service-node-port-range:
    - 30000-32767
  shutdown-delay-duration:
    - 70s # give SDN some time to converge: 30s for iptable lock contention, 25s for the second try and some seconds for AWS to update ELBs
  storage-backend:
    - etcd3
  storage-media-type:
    - application/vnd.kubernetes.protobuf
  tls-cert-file:
    - /etc/kubernetes/static-pod-certs/secrets/service-network-serving-certkey/tls.crt
  tls-private-key-file:
    - /etc/kubernetes/static-pod-certs/secrets/service-network-serving-certkey/tls.key
authConfig:
  oauthMetadataFile: ""
consolePublicURL: ""
projectConfig:
  defaultNodeSelector: ""
servicesSubnet: 10.3.0.0/16 # ServiceCIDR # set by observe_network.go
servingInfo:
  bindAddress: 0.0.0.0:6443 # set by observe_network.go
  bindNetwork: tcp4 # set by observe_network.go
  namedCertificates: null # set by observe_apiserver.go
`)

func v410ConfigDefaultconfigYamlBytes() ([]byte, error) {
	return _v410ConfigDefaultconfigYaml, nil
}

func v410ConfigDefaultconfigYaml() (*asset, error) {
	bytes, err := v410ConfigDefaultconfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/config/defaultconfig.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverApiserverOpenshiftIo_apirequestcountYaml = []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    include.release.openshift.io/self-managed-high-availability: "true"
    include.release.openshift.io/single-node-developer: "true"
  name: apirequestcounts.apiserver.openshift.io
spec:
  group: apiserver.openshift.io
  names:
    kind: APIRequestCount
    listKind: APIRequestCountList
    plural: apirequestcounts
    singular: apirequestcount
  scope: Cluster
  versions:
  - name: v1
    served: true
    storage: true
    subresources:
      status: {}
    additionalPrinterColumns:
    - name: RemovedInRelease
      type: string
      description: Release in which an API will be removed.
      jsonPath: .status.removedInRelease
    - name: RequestsInCurrentHour
      type: integer
      description: Number of requests in the current hour.
      jsonPath: .status.currentHour.requestCount
    - name: RequestsInLast24h
      type: integer
      description: Number of requests in the last 24h.
      jsonPath: .status.requestCount
    "schema":
      "openAPIV3Schema":
        description: APIRequestCount tracks requests made to an API. The instance
          name must be of the form ` + "`" + `resource.version.group` + "`" + `, matching the resource.
        type: object
        required:
        - spec
        properties:
          apiVersion:
            description: 'APIVersion defines the versioned schema of this representation
              of an object. Servers should convert recognized schemas to the latest
              internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources'
            type: string
          kind:
            description: 'Kind is a string value representing the REST resource this
              object represents. Servers may infer this from the endpoint the client
              submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds'
            type: string
          metadata:
            type: object
          spec:
            description: spec defines the characteristics of the resource.
            type: object
            properties:
              numberOfUsersToReport:
                description: numberOfUsersToReport is the number of users to include
                  in the report. If unspecified or zero, the default is ten.  This
                  is default is subject to change.
                type: integer
                format: int64
                default: 10
                maximum: 100
                minimum: 0
          status:
            description: status contains the observed state of the resource.
            type: object
            properties:
              conditions:
                description: conditions contains details of the current status of
                  this API Resource.
                type: array
                items:
                  description: "Condition contains details for one aspect of the current
                    state of this API Resource. --- This struct is intended for direct
                    use as an array at the field path .status.conditions.  For example,
                    type FooStatus struct{     // Represents the observations of a
                    foo's current state.     // Known .status.conditions.type are:
                    \"Available\", \"Progressing\", and \"Degraded\"     // +patchMergeKey=type
                    \    // +patchStrategy=merge     // +listType=map     // +listMapKey=type
                    \    Conditions []metav1.Condition ` + "`" + `json:\"conditions,omitempty\"
                    patchStrategy:\"merge\" patchMergeKey:\"type\" protobuf:\"bytes,1,rep,name=conditions\"` + "`" + `
                    \n     // other fields }"
                  type: object
                  required:
                  - lastTransitionTime
                  - message
                  - reason
                  - status
                  - type
                  properties:
                    lastTransitionTime:
                      description: lastTransitionTime is the last time the condition
                        transitioned from one status to another. This should be when
                        the underlying condition changed.  If that is not known, then
                        using the time when the API field changed is acceptable.
                      type: string
                      format: date-time
                    message:
                      description: message is a human readable message indicating
                        details about the transition. This may be an empty string.
                      type: string
                      maxLength: 32768
                    observedGeneration:
                      description: observedGeneration represents the .metadata.generation
                        that the condition was set based upon. For instance, if .metadata.generation
                        is currently 12, but the .status.conditions[x].observedGeneration
                        is 9, the condition is out of date with respect to the current
                        state of the instance.
                      type: integer
                      format: int64
                      minimum: 0
                    reason:
                      description: reason contains a programmatic identifier indicating
                        the reason for the condition's last transition. Producers
                        of specific condition types may define expected values and
                        meanings for this field, and whether the values are considered
                        a guaranteed API. The value should be a CamelCase string.
                        This field may not be empty.
                      type: string
                      maxLength: 1024
                      minLength: 1
                      pattern: ^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$
                    status:
                      description: status of the condition, one of True, False, Unknown.
                      type: string
                      enum:
                      - "True"
                      - "False"
                      - Unknown
                    type:
                      description: type of condition in CamelCase or in foo.example.com/CamelCase.
                        --- Many .condition.type values are consistent across resources
                        like Available, but because arbitrary conditions can be useful
                        (see .node.status.conditions), the ability to deconflict is
                        important. The regex it matches is (dns1123SubdomainFmt/)?(qualifiedNameFmt)
                      type: string
                      maxLength: 316
                      pattern: ^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$
              currentHour:
                description: currentHour contains request history for the current
                  hour. This is porcelain to make the API easier to read by humans
                  seeing if they addressed a problem. This field is reset on the hour.
                type: object
                properties:
                  byNode:
                    description: byNode contains logs of requests per node.
                    type: array
                    maxItems: 512
                    items:
                      description: PerNodeAPIRequestLog contains logs of requests
                        to a certain node.
                      type: object
                      properties:
                        byUser:
                          description: byUser contains request details by top .spec.numberOfUsersToReport
                            users. Note that because in the case of an apiserver,
                            restart the list of top users is determined on a best-effort
                            basis, the list might be imprecise. In addition, some
                            system users may be explicitly included in the list.
                          type: array
                          maxItems: 500
                          items:
                            description: PerUserAPIRequestCount contains logs of a
                              user's requests.
                            type: object
                            properties:
                              byVerb:
                                description: byVerb details by verb.
                                type: array
                                maxItems: 10
                                items:
                                  description: PerVerbAPIRequestCount requestCounts
                                    requests by API request verb.
                                  type: object
                                  properties:
                                    requestCount:
                                      description: requestCount of requests for verb.
                                      type: integer
                                      format: int64
                                      minimum: 0
                                    verb:
                                      description: verb of API request (get, list,
                                        create, etc...)
                                      type: string
                                      maxLength: 20
                              requestCount:
                                description: requestCount of requests by the user
                                  across all verbs.
                                type: integer
                                format: int64
                                minimum: 0
                              userAgent:
                                description: userAgent that made the request. The
                                  same user often has multiple binaries which connect
                                  (pods with many containers).  The different binaries
                                  will have different userAgents, but the same user.  In
                                  addition, we have userAgents with version information
                                  embedded and the userName isn't likely to change.
                                type: string
                                maxLength: 1024
                              username:
                                description: userName that made the request.
                                type: string
                                maxLength: 512
                        nodeName:
                          description: nodeName where the request are being handled.
                          type: string
                          maxLength: 512
                          minLength: 1
                        requestCount:
                          description: requestCount is a sum of all requestCounts
                            across all users, even those outside of the top 10 users.
                          type: integer
                          format: int64
                          minimum: 0
                  requestCount:
                    description: requestCount is a sum of all requestCounts across
                      nodes.
                    type: integer
                    format: int64
                    minimum: 0
              last24h:
                description: last24h contains request history for the last 24 hours,
                  indexed by the hour, so 12:00AM-12:59 is in index 0, 6am-6:59am
                  is index 6, etc. The index of the current hour is updated live and
                  then duplicated into the requestsLastHour field.
                type: array
                maxItems: 24
                items:
                  description: PerResourceAPIRequestLog logs request for various nodes.
                  type: object
                  properties:
                    byNode:
                      description: byNode contains logs of requests per node.
                      type: array
                      maxItems: 512
                      items:
                        description: PerNodeAPIRequestLog contains logs of requests
                          to a certain node.
                        type: object
                        properties:
                          byUser:
                            description: byUser contains request details by top .spec.numberOfUsersToReport
                              users. Note that because in the case of an apiserver,
                              restart the list of top users is determined on a best-effort
                              basis, the list might be imprecise. In addition, some
                              system users may be explicitly included in the list.
                            type: array
                            maxItems: 500
                            items:
                              description: PerUserAPIRequestCount contains logs of
                                a user's requests.
                              type: object
                              properties:
                                byVerb:
                                  description: byVerb details by verb.
                                  type: array
                                  maxItems: 10
                                  items:
                                    description: PerVerbAPIRequestCount requestCounts
                                      requests by API request verb.
                                    type: object
                                    properties:
                                      requestCount:
                                        description: requestCount of requests for
                                          verb.
                                        type: integer
                                        format: int64
                                        minimum: 0
                                      verb:
                                        description: verb of API request (get, list,
                                          create, etc...)
                                        type: string
                                        maxLength: 20
                                requestCount:
                                  description: requestCount of requests by the user
                                    across all verbs.
                                  type: integer
                                  format: int64
                                  minimum: 0
                                userAgent:
                                  description: userAgent that made the request. The
                                    same user often has multiple binaries which connect
                                    (pods with many containers).  The different binaries
                                    will have different userAgents, but the same user.  In
                                    addition, we have userAgents with version information
                                    embedded and the userName isn't likely to change.
                                  type: string
                                  maxLength: 1024
                                username:
                                  description: userName that made the request.
                                  type: string
                                  maxLength: 512
                          nodeName:
                            description: nodeName where the request are being handled.
                            type: string
                            maxLength: 512
                            minLength: 1
                          requestCount:
                            description: requestCount is a sum of all requestCounts
                              across all users, even those outside of the top 10 users.
                            type: integer
                            format: int64
                            minimum: 0
                    requestCount:
                      description: requestCount is a sum of all requestCounts across
                        nodes.
                      type: integer
                      format: int64
                      minimum: 0
              removedInRelease:
                description: removedInRelease is when the API will be removed.
                type: string
                maxLength: 64
                minLength: 0
                pattern: ^[0-9][0-9]*\.[0-9][0-9]*$
              requestCount:
                description: requestCount is a sum of all requestCounts across all
                  current hours, nodes, and users.
                type: integer
                format: int64
                minimum: 0
`)

func v410KubeApiserverApiserverOpenshiftIo_apirequestcountYamlBytes() ([]byte, error) {
	return _v410KubeApiserverApiserverOpenshiftIo_apirequestcountYaml, nil
}

func v410KubeApiserverApiserverOpenshiftIo_apirequestcountYaml() (*asset, error) {
	bytes, err := v410KubeApiserverApiserverOpenshiftIo_apirequestcountYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterroleCrdReaderYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: system:openshift:controller:check-endpoints-crd-reader
rules:
  - resources:
      - customresourcedefinitions
    apiGroups:
      - apiextensions.k8s.io
    verbs:
      - get
      - list
      - watch
`)

func v410KubeApiserverCheckEndpointsClusterroleCrdReaderYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterroleCrdReaderYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterroleCrdReaderYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterroleCrdReaderYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrole-crd-reader.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterroleNodeReaderYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: system:openshift:controller:check-endpoints-node-reader
rules:
  - resources:
      - nodes
    apiGroups:
      - ""
    verbs:
      - get
      - list
      - watch
`)

func v410KubeApiserverCheckEndpointsClusterroleNodeReaderYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterroleNodeReaderYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterroleNodeReaderYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterroleNodeReaderYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrole-node-reader.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterroleYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: system:openshift:controller:check-endpoints
rules:
  - resources:
      - podnetworkconnectivitychecks
    apiGroups:
      - controlplane.operator.openshift.io
    verbs:
      - get
      - list
      - watch
  - resources:
      - podnetworkconnectivitychecks/status
    apiGroups:
      - controlplane.operator.openshift.io
    verbs:
      - get
      - list
      - patch
      - update
      - watch
  - resources:
      - pods
      - secrets
    apiGroups:
      - ""
    verbs:
      - get
      - list
      - watch
  - resources:
      - events
    apiGroups:
      - ""
    verbs:
      - get
      - list
      - watch
      - create
      - update
      - patch
`)

func v410KubeApiserverCheckEndpointsClusterroleYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterroleYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterroleYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterroleYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrole.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:controller:kube-apiserver-check-endpoints-auth-delegator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: User
    name: system:serviceaccount:openshift-kube-apiserver:check-endpoints
`)

func v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-auth-delegator.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:controller:kube-apiserver-check-endpoints-crd-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:controller:check-endpoints-crd-reader
subjects:
  - kind: User
    name: system:serviceaccount:openshift-kube-apiserver:check-endpoints
`)

func v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-crd-reader.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:controller:kube-apiserver-check-endpoints-node-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:controller:check-endpoints-node-reader
subjects:
  - kind: User
    name: system:serviceaccount:openshift-kube-apiserver:check-endpoints
`)

func v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYaml, nil
}

func v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-node-reader.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsKubeconfigCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: check-endpoints-kubeconfig
  namespace: openshift-kube-apiserver
data:
  kubeconfig: |
    apiVersion: v1
    clusters:
      - cluster:
          certificate-authority: /etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-server-ca/ca-bundle.crt
          server: https://localhost:6443
        name: loopback
    contexts:
      - context:
          cluster: loopback
          user: check-endpoints
        name: check-endpoints
    current-context: check-endpoints
    kind: Config
    preferences: {}
    users:
      - name: check-endpoints
        user:
          client-certificate: /etc/kubernetes/static-pod-certs/secrets/check-endpoints-client-cert-key/tls.crt
          client-key: /etc/kubernetes/static-pod-certs/secrets/check-endpoints-client-cert-key/tls.key
`)

func v410KubeApiserverCheckEndpointsKubeconfigCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsKubeconfigCmYaml, nil
}

func v410KubeApiserverCheckEndpointsKubeconfigCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsKubeconfigCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-kubeconfig-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsRolebindingKubeSystemYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: system:openshift:controller:kube-apiserver-check-endpoints
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: extension-apiserver-authentication-reader
subjects:
  - kind: User
    name: system:serviceaccount:openshift-kube-apiserver:check-endpoints
`)

func v410KubeApiserverCheckEndpointsRolebindingKubeSystemYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsRolebindingKubeSystemYaml, nil
}

func v410KubeApiserverCheckEndpointsRolebindingKubeSystemYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsRolebindingKubeSystemYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-rolebinding-kube-system.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCheckEndpointsRolebindingYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: system:openshift:controller:check-endpoints
  namespace: openshift-kube-apiserver
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:controller:check-endpoints
subjects:
  - kind: User
    name: system:serviceaccount:openshift-kube-apiserver:check-endpoints
`)

func v410KubeApiserverCheckEndpointsRolebindingYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCheckEndpointsRolebindingYaml, nil
}

func v410KubeApiserverCheckEndpointsRolebindingYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCheckEndpointsRolebindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/check-endpoints-rolebinding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: config
data:
  config.yaml:
`)

func v410KubeApiserverCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverCmYaml, nil
}

func v410KubeApiserverCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverControlPlaneNodeKubeconfigCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: control-plane-node-kubeconfig
  namespace: openshift-kube-apiserver
data:
  kubeconfig: |
    apiVersion: v1
    clusters:
      - cluster:
          certificate-authority: /etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-server-ca/ca-bundle.crt
          server: https://localhost:6443
        name: loopback
    contexts:
      - context:
          cluster: loopback
          user: control-plane-node
        name: control-plane-node
    current-context: control-plane-node
    kind: Config
    preferences: {}
    users:
      - name: control-plane-node
        user:
          client-certificate: /etc/kubernetes/static-pod-certs/secrets/control-plane-node-admin-client-cert-key/tls.crt
          client-key: /etc/kubernetes/static-pod-certs/secrets/control-plane-node-admin-client-cert-key/tls.key
`)

func v410KubeApiserverControlPlaneNodeKubeconfigCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverControlPlaneNodeKubeconfigCmYaml, nil
}

func v410KubeApiserverControlPlaneNodeKubeconfigCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverControlPlaneNodeKubeconfigCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/control-plane-node-kubeconfig-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYaml = []byte(`# this rolebinding allows access to the in-cluster CA bundles for authentication, the request header flags, and
# the front-proxy CA configuration so that anyone can set up a DelegatingAuthenticator that can terminate
# client certificates.
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: authentication-reader-for-authenticated-users
  namespace: kube-system
roleRef:
  kind: Role
  name: extension-apiserver-authentication-reader
  apiGroup: rbac.authorization.k8s.io
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: system:authenticated
`)

func v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYamlBytes() ([]byte, error) {
	return _v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYaml, nil
}

func v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYaml() (*asset, error) {
	bytes, err := v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/delegated-incluster-authentication-rolebinding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverKubeconfigCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: kube-apiserver-cert-syncer-kubeconfig
  namespace: openshift-kube-apiserver
data:
  kubeconfig: |
    apiVersion: v1
    clusters:
      - cluster:
          certificate-authority: /etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-server-ca/ca-bundle.crt
          server: https://localhost:6443
          tls-server-name: localhost-recovery
        name: loopback
    contexts:
      - context:
          cluster: loopback
          user: kube-apiserver-cert-syncer
        name: kube-apiserver-cert-syncer
    current-context: kube-apiserver-cert-syncer
    kind: Config
    preferences: {}
    users:
      - name: kube-apiserver-cert-syncer
        user:
          tokenFile: /etc/kubernetes/static-pod-resources/secrets/localhost-recovery-client-token/token
`)

func v410KubeApiserverKubeconfigCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverKubeconfigCmYaml, nil
}

func v410KubeApiserverKubeconfigCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverKubeconfigCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/kubeconfig-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverLocalhostRecoveryClientCrbYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:operator:kube-apiserver-recovery
roleRef:
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: localhost-recovery-client
  namespace: openshift-kube-apiserver
`)

func v410KubeApiserverLocalhostRecoveryClientCrbYamlBytes() ([]byte, error) {
	return _v410KubeApiserverLocalhostRecoveryClientCrbYaml, nil
}

func v410KubeApiserverLocalhostRecoveryClientCrbYaml() (*asset, error) {
	bytes, err := v410KubeApiserverLocalhostRecoveryClientCrbYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/localhost-recovery-client-crb.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverLocalhostRecoverySaYaml = []byte(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: localhost-recovery-client
  namespace: openshift-kube-apiserver
`)

func v410KubeApiserverLocalhostRecoverySaYamlBytes() ([]byte, error) {
	return _v410KubeApiserverLocalhostRecoverySaYaml, nil
}

func v410KubeApiserverLocalhostRecoverySaYaml() (*asset, error) {
	bytes, err := v410KubeApiserverLocalhostRecoverySaYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/localhost-recovery-sa.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverLocalhostRecoveryTokenYaml = []byte(`apiVersion: v1
kind: Secret
metadata:
  name: localhost-recovery-client-token
  namespace: openshift-kube-apiserver
  annotations:
    kubernetes.io/service-account.name: localhost-recovery-client
type: kubernetes.io/service-account-token
`)

func v410KubeApiserverLocalhostRecoveryTokenYamlBytes() ([]byte, error) {
	return _v410KubeApiserverLocalhostRecoveryTokenYaml, nil
}

func v410KubeApiserverLocalhostRecoveryTokenYaml() (*asset, error) {
	bytes, err := v410KubeApiserverLocalhostRecoveryTokenYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/localhost-recovery-token.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverNodeKubeconfigsYaml = []byte(`apiVersion: v1
kind: Secret
metadata:
  name: node-kubeconfigs
  namespace: openshift-kube-apiserver
stringData:
  localhost.kubeconfig: |
    apiVersion: v1
    kind: Config
    clusters:
    - cluster:
        certificate-authority-data: $CA_DATA
        server: https://localhost:6443
      name: localhost
    contexts:
    - context:
        cluster: localhost
        user: system:admin
      name: system:admin
    current-context: system:admin
    users:
    - name: system:admin
      user:
        client-certificate-data: $SYSTEM_ADMIN_CERT_DATA
        client-key-data: $SYSTEM_ADMIN_KEY_DATA
  localhost-recovery.kubeconfig: |
    apiVersion: v1
    kind: Config
    clusters:
    - cluster:
        certificate-authority-data: $CA_DATA
        server: https://localhost:6443
        tls-server-name: localhost-recovery
      name: localhost-recovery
    contexts:
    - context:
        cluster: localhost-recovery
        user: system:admin
      name: system:admin
    current-context: system:admin
    users:
    - name: system:admin
      user:
        client-certificate-data: $SYSTEM_ADMIN_CERT_DATA
        client-key-data: $SYSTEM_ADMIN_KEY_DATA
  lb-ext.kubeconfig: |
    apiVersion: v1
    kind: Config
    clusters:
    - cluster:
        certificate-authority-data: $CA_DATA
        server: $LB-EXT
      name: lb-ext
    contexts:
    - context:
        cluster: lb-ext
        user: system:admin
      name: system:admin
    current-context: system:admin
    users:
    - name: system:admin
      user:
        client-certificate-data: $SYSTEM_ADMIN_CERT_DATA
        client-key-data: $SYSTEM_ADMIN_KEY_DATA
  lb-int.kubeconfig: |
    apiVersion: v1
    kind: Config
    clusters:
    - cluster:
        certificate-authority-data: $CA_DATA
        server: $LB-INT
      name: lb-int
    contexts:
    - context:
        cluster: lb-int
        user: system:admin
      name: system:admin
    current-context: system:admin
    users:
    - name: system:admin
      user:
        client-certificate-data: $SYSTEM_ADMIN_CERT_DATA
        client-key-data: $SYSTEM_ADMIN_KEY_DATA
`)

func v410KubeApiserverNodeKubeconfigsYamlBytes() ([]byte, error) {
	return _v410KubeApiserverNodeKubeconfigsYaml, nil
}

func v410KubeApiserverNodeKubeconfigsYaml() (*asset, error) {
	bytes, err := v410KubeApiserverNodeKubeconfigsYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/node-kubeconfigs.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverNsYaml = []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: openshift-kube-apiserver
  annotations:
    openshift.io/node-selector: ""
    workload.openshift.io/allowed: "management"
  labels:
    openshift.io/run-level: "0"
    openshift.io/cluster-monitoring: "true"
`)

func v410KubeApiserverNsYamlBytes() ([]byte, error) {
	return _v410KubeApiserverNsYaml, nil
}

func v410KubeApiserverNsYaml() (*asset, error) {
	bytes, err := v410KubeApiserverNsYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/ns.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverPodCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: kube-apiserver-pod
data:
  pod.yaml:
  forceRedeploymentReason:
  version:
`)

func v410KubeApiserverPodCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverPodCmYaml, nil
}

func v410KubeApiserverPodCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverPodCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/pod-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  namespace: openshift-kube-apiserver
  name: kube-apiserver
  annotations:
    kubectl.kubernetes.io/default-logs-container: kube-apiserver
    target.workload.openshift.io/management: '{"effect": "PreferredDuringScheduling"}'
  labels:
    app: openshift-kube-apiserver
    apiserver: "true"
    revision: "REVISION"
spec:
  initContainers:
    - name: setup
      terminationMessagePolicy: FallbackToLogsOnError
      image: {{.Image}}
      imagePullPolicy: IfNotPresent
      volumeMounts:
        - mountPath: /var/log/kube-apiserver
          name: audit-dir
      command: ['/usr/bin/timeout', '105', '/bin/bash', '-ec'] # a bit more than 60s for graceful termination + 35s for minimum-termination-duration, 5s extra cri-o's graceful termination period
      args:
      - |
        echo -n "Fixing audit permissions."
        chmod 0700 /var/log/kube-apiserver && touch /var/log/kube-apiserver/audit.log && chmod 0600 /var/log/kube-apiserver/*
        echo -n "Waiting for port :6443 and :6080 to be released."
        while [ -n "$(ss -Htan '( sport = 6443 or sport = 6080 )')" ]; do
          echo -n "."
          sleep 1
        done
      securityContext:
        privileged: true
      resources:
        requests:
          memory: 50Mi
          cpu: 5m
  containers:
  - name: kube-apiserver
    image: {{.Image}}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["/bin/bash", "-ec"]
    args:
        - |
          LOCK=/var/log/kube-apiserver/.lock
          echo -n "Acquiring exclusive lock ${LOCK}"
          exec {LOCK_FD}>${LOCK} && flock -n "${LOCK_FD}" || {
            echo "$(date -Iseconds -u) kubelet did not terminate old kube-apiserver before new one" >> /var/log/kube-apiserver/lock.log
            echo -n ": WARNING: kubelet did not terminate old kube-apiserver before new one."
            # we didn't get an exclusive lock. We keep going with the risk to corrupt audit logs.
          }
          echo

          if [ -f /etc/kubernetes/static-pod-certs/configmaps/trusted-ca-bundle/ca-bundle.crt ]; then
            echo "Copying system trust bundle"
            cp -f /etc/kubernetes/static-pod-certs/configmaps/trusted-ca-bundle/ca-bundle.crt /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
          fi
          echo -n "Waiting for port :6443 to be released."
          tries=0
          while [ -n "$(ss -Htan '( sport = 6443 )')" ]; do
            echo -n "."
            sleep 1
            (( tries += 1 ))
            if [[ "${tries}" -gt 105 ]]; then
              echo "timed out waiting for port :6443 to be released"
              exit 1
            fi
          done
          echo
          exec watch-termination --termination-touch-file=/var/log/kube-apiserver/.terminating --termination-log-file=/var/log/kube-apiserver/termination.log --graceful-termination-duration={{.GracefulTerminationDuration}}s --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-cert-syncer-kubeconfig/kubeconfig -- hyperkube kube-apiserver --openshift-config=/etc/kubernetes/static-pod-resources/configmaps/config/config.yaml --advertise-address=${HOST_IP} {{.Verbosity}} --permit-address-sharing
    resources:
      requests:
        memory: 1Gi
        cpu: 265m
    ports:
    - containerPort: 6443
    volumeMounts:
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
    - mountPath: /etc/kubernetes/static-pod-certs
      name: cert-dir
    - mountPath: /var/log/kube-apiserver
      name: audit-dir
    livenessProbe:
      httpGet:
        scheme: HTTPS
        port: 6443
        path: livez
      initialDelaySeconds: 45
      timeoutSeconds: 10
    readinessProbe:
      httpGet:
        scheme: HTTPS
        port: 6443
        path: readyz
      initialDelaySeconds: 10
      timeoutSeconds: 10
    env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      - name: POD_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
      - name: STATIC_POD_VERSION # Avoid using 'REVISION' here otherwise it will be substituted
        value: REVISION
      - name: HOST_IP
        valueFrom:
          fieldRef:
            fieldPath: status.hostIP
    securityContext:
      privileged: true
  - name: kube-apiserver-cert-syncer
    env:
    - name: POD_NAME
      valueFrom:
        fieldRef:
          fieldPath: metadata.name
    - name: POD_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    image: {{.OperatorImage}}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["cluster-kube-apiserver-operator", "cert-syncer"]
    args:
      - --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-cert-syncer-kubeconfig/kubeconfig
      - --namespace=$(POD_NAMESPACE)
      - --destination-dir=/etc/kubernetes/static-pod-certs
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
    volumeMounts:
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
    - mountPath: /etc/kubernetes/static-pod-certs
      name: cert-dir
  - name: kube-apiserver-cert-regeneration-controller
    env:
    - name: POD_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    image: {{.OperatorImage}}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["cluster-kube-apiserver-operator", "cert-regeneration-controller"]
    args:
      - --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-cert-syncer-kubeconfig/kubeconfig
      - --namespace=$(POD_NAMESPACE)
      - -v=2
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
    volumeMounts:
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
  - name: kube-apiserver-insecure-readyz
    image: {{.OperatorImage}}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["cluster-kube-apiserver-operator", "insecure-readyz"]
    args:
    - --insecure-port=6080
    - --delegate-url=https://localhost:6443/readyz
    ports:
    - containerPort: 6080
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
  - name: kube-apiserver-check-endpoints
    image: {{.OperatorImage}}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command:
      - cluster-kube-apiserver-operator
      - check-endpoints
    args:
      - --kubeconfig
      - /etc/kubernetes/static-pod-certs/configmaps/check-endpoints-kubeconfig/kubeconfig
      - --listen
      - 0.0.0.0:17697
      - --namespace
      - $(POD_NAMESPACE)
      - --v
      - '2'
    env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      - name: POD_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
    volumeMounts:
      - mountPath: /etc/kubernetes/static-pod-resources
        name: resource-dir
      - mountPath: /etc/kubernetes/static-pod-certs
        name: cert-dir
    ports:
      - name: check-endpoints
        hostPort: 17697
        containerPort: 17697
        protocol: TCP
    livenessProbe:
      httpGet:
        scheme: HTTPS
        port: 17697
        path: healthz
      initialDelaySeconds: 10
      timeoutSeconds: 10
    readinessProbe:
      httpGet:
        scheme: HTTPS
        port: 17697
        path: healthz
      initialDelaySeconds: 10
      timeoutSeconds: 10
    resources:
      requests:
        memory: 50Mi
        cpu: 10m
  terminationGracePeriodSeconds: {{.GracefulTerminationDuration}}
  hostNetwork: true
  priorityClassName: system-node-critical
  tolerations:
  - operator: "Exists"
  volumes:
  - hostPath:
      path: /etc/kubernetes/static-pod-resources/kube-apiserver-pod-REVISION
    name: resource-dir
  - hostPath:
      path: /etc/kubernetes/static-pod-resources/kube-apiserver-certs
    name: cert-dir
  - hostPath:
      path: /var/log/kube-apiserver
    name: audit-dir
`)

func v410KubeApiserverPodYamlBytes() ([]byte, error) {
	return _v410KubeApiserverPodYaml, nil
}

func v410KubeApiserverPodYaml() (*asset, error) {
	bytes, err := v410KubeApiserverPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverRecoveryConfigYaml = []byte(`apiVersion: kubecontrolplane.config.openshift.io/v1
kind: KubeAPIServerConfig
apiServerArguments:
  storage-backend:
  - etcd3
  storage-media-type:
  - application/vnd.kubernetes.protobuf
  encryption-provider-config:
    - /etc/kubernetes/static-pod-resources/encryption-config
servingInfo:
  bindAddress: 127.0.0.1:7443
  bindNetwork: tcp4
  certFile: /etc/kubernetes/static-pod-resources/serving-ca.crt
  keyFile: /etc/kubernetes/static-pod-resources/serving-ca.key
  clientCA: /etc/kubernetes/static-pod-resources/serving-ca.crt
storageConfig:
  keyFile: /etc/kubernetes/static-pod-resources/etcd-client.key
  certFile: /etc/kubernetes/static-pod-resources/etcd-client.crt
  ca: /etc/kubernetes/static-pod-resources/etcd-serving-ca-bundle.crt
  urls:
  - "https://localhost:2379"

# Make our modified kube-apiserver happy.
# (Everything bellow this line is just to provide some certs file
# because our modified kube-apiserver tries to read those even if you don't want to set them up.)
authConfig:
  oauthMetadataFile: ""
  requestHeader:
    clientCA: /etc/kubernetes/static-pod-resources/serving-ca.crt
serviceAccountPublicKeyFiles:
- /etc/kubernetes/static-pod-resources/serving-ca.crt
kubeletClientInfo:
  ca: /etc/kubernetes/static-pod-resources/serving-ca.crt
  certFile: /etc/kubernetes/static-pod-resources/serving-ca.crt
  keyFile: /etc/kubernetes/static-pod-resources/serving-ca.key
aggregatorConfig:
  proxyClientInfo:
    certFile: /etc/kubernetes/static-pod-resources/serving-ca.crt
    keyFile: /etc/kubernetes/static-pod-resources/serving-ca.key
`)

func v410KubeApiserverRecoveryConfigYamlBytes() ([]byte, error) {
	return _v410KubeApiserverRecoveryConfigYaml, nil
}

func v410KubeApiserverRecoveryConfigYaml() (*asset, error) {
	bytes, err := v410KubeApiserverRecoveryConfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/recovery-config.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverRecoveryEncryptionConfigYaml = []byte(`apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
`)

func v410KubeApiserverRecoveryEncryptionConfigYamlBytes() ([]byte, error) {
	return _v410KubeApiserverRecoveryEncryptionConfigYaml, nil
}

func v410KubeApiserverRecoveryEncryptionConfigYaml() (*asset, error) {
	bytes, err := v410KubeApiserverRecoveryEncryptionConfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/recovery-encryption-config.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverRecoveryPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  namespace: openshift-kube-apiserver
  name: kube-apiserver-recovery
  labels:
    revision: "recovery"
  annotations:
    target.workload.openshift.io/management: '{"effect": "PreferredDuringScheduling"}'
spec:
  containers:
  - name: kube-apiserver-recovery
    image: "{{ .KubeApiserverImage }}"
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["hyperkube", "kube-apiserver"]
    args:
    - --openshift-config=/etc/kubernetes/static-pod-resources/config.yaml
    resources:
      requests:
        memory: 1Gi
        cpu: 150m
    ports:
    - containerPort: 7443
    volumeMounts:
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
  terminationGracePeriodSeconds: 0
  hostNetwork: true
  priorityClassName: system-node-critical
  tolerations:
  - operator: "Exists"
  volumes:
  - hostPath:
      path: "{{ .ResourceDir }}"
    name: resource-dir
`)

func v410KubeApiserverRecoveryPodYamlBytes() ([]byte, error) {
	return _v410KubeApiserverRecoveryPodYaml, nil
}

func v410KubeApiserverRecoveryPodYaml() (*asset, error) {
	bytes, err := v410KubeApiserverRecoveryPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/recovery-pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverRolloutMonitorPodCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: rollout-monitor-pod
data:
  pod.yaml:
`)

func v410KubeApiserverRolloutMonitorPodCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverRolloutMonitorPodCmYaml, nil
}

func v410KubeApiserverRolloutMonitorPodCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverRolloutMonitorPodCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/rollout-monitor-pod-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverRolloutMonitorPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  namespace: openshift-kube-apiserver
  name: rollout-monitor
  labels:
    revision: "REVISION"
spec:
  containers:
  - name: rollout-monitor
    image: ${OPERATOR_IMAGE}
    imagePullPolicy: IfNotPresent
    command: ["cluster-kube-apiserver-operator", "rollout-monitor"]
    args:
      - -v=3
    volumeMounts:
    - mountPath: /etc/kubernetes/manifests
      name: manifests
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
    securityContext:
      privileged: true
  hostNetwork: true
  priorityClassName: system-node-critical
  tolerations:
  - operator: "Exists"
  volumes:
  - name: manifests
    hostPath:
      path: /etc/kubernetes/manifests
`)

func v410KubeApiserverRolloutMonitorPodYamlBytes() ([]byte, error) {
	return _v410KubeApiserverRolloutMonitorPodYaml, nil
}

func v410KubeApiserverRolloutMonitorPodYaml() (*asset, error) {
	bytes, err := v410KubeApiserverRolloutMonitorPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/rollout-monitor-pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverStorageVersionMigrationFlowschemaYaml = []byte(`apiVersion: migration.k8s.io/v1alpha1
kind: StorageVersionMigration
metadata:
  name: flowcontrol-flowschema-storage-version-migration
spec:
  resource:
    group: flowcontrol.apiserver.k8s.io
    version: v1beta1
    resource: flowschemas
`)

func v410KubeApiserverStorageVersionMigrationFlowschemaYamlBytes() ([]byte, error) {
	return _v410KubeApiserverStorageVersionMigrationFlowschemaYaml, nil
}

func v410KubeApiserverStorageVersionMigrationFlowschemaYaml() (*asset, error) {
	bytes, err := v410KubeApiserverStorageVersionMigrationFlowschemaYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/storage-version-migration-flowschema.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYaml = []byte(`apiVersion: migration.k8s.io/v1alpha1
kind: StorageVersionMigration
metadata:
  name: flowcontrol-prioritylevel-storage-version-migration
spec:
  resource:
    group: flowcontrol.apiserver.k8s.io
    version: v1beta1
    resource: prioritylevelconfigurations
`)

func v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYamlBytes() ([]byte, error) {
	return _v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYaml, nil
}

func v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYaml() (*asset, error) {
	bytes, err := v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/storage-version-migration-prioritylevelconfiguration.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverSvcYaml = []byte(`apiVersion: v1
kind: Service
metadata:
  namespace: openshift-kube-apiserver
  name: apiserver
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/scheme: https
spec:
  type: ClusterIP
  selector:
    apiserver: "true"
  ports:
  - name: https
    port: 443
    targetPort: 6443
`)

func v410KubeApiserverSvcYamlBytes() ([]byte, error) {
	return _v410KubeApiserverSvcYaml, nil
}

func v410KubeApiserverSvcYaml() (*asset, error) {
	bytes, err := v410KubeApiserverSvcYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/svc.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeApiserverTrustedCaCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: trusted-ca-bundle
  labels:
    config.openshift.io/inject-trusted-cabundle: "true"
`)

func v410KubeApiserverTrustedCaCmYamlBytes() ([]byte, error) {
	return _v410KubeApiserverTrustedCaCmYaml, nil
}

func v410KubeApiserverTrustedCaCmYaml() (*asset, error) {
	bytes, err := v410KubeApiserverTrustedCaCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-apiserver/trusted-ca-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("Asset %s can't read by error: %v", name, err)
		}
		return a.bytes, nil
	}
	return nil, fmt.Errorf("Asset %s not found", name)
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}

// AssetInfo loads and returns the asset info for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func AssetInfo(name string) (os.FileInfo, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("AssetInfo %s can't read by error: %v", name, err)
		}
		return a.info, nil
	}
	return nil, fmt.Errorf("AssetInfo %s not found", name)
}

// AssetNames returns the names of the assets.
func AssetNames() []string {
	names := make([]string, 0, len(_bindata))
	for name := range _bindata {
		names = append(names, name)
	}
	return names
}

// _bindata is a table, holding each asset generator, mapped to its name.
var _bindata = map[string]func() (*asset, error){
	"v4.1.0/alerts/api-usage.yaml":                                                    v410AlertsApiUsageYaml,
	"v4.1.0/alerts/cpu-utilization.yaml":                                              v410AlertsCpuUtilizationYaml,
	"v4.1.0/alerts/kube-apiserver-requests.yaml":                                      v410AlertsKubeApiserverRequestsYaml,
	"v4.1.0/alerts/kube-apiserver-slos.yaml":                                          v410AlertsKubeApiserverSlosYaml,
	"v4.1.0/config/config-overrides.yaml":                                             v410ConfigConfigOverridesYaml,
	"v4.1.0/config/defaultconfig.yaml":                                                v410ConfigDefaultconfigYaml,
	"v4.1.0/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml":               v410KubeApiserverApiserverOpenshiftIo_apirequestcountYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrole-crd-reader.yaml":               v410KubeApiserverCheckEndpointsClusterroleCrdReaderYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrole-node-reader.yaml":              v410KubeApiserverCheckEndpointsClusterroleNodeReaderYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrole.yaml":                          v410KubeApiserverCheckEndpointsClusterroleYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-auth-delegator.yaml":    v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-crd-reader.yaml":        v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYaml,
	"v4.1.0/kube-apiserver/check-endpoints-clusterrolebinding-node-reader.yaml":       v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYaml,
	"v4.1.0/kube-apiserver/check-endpoints-kubeconfig-cm.yaml":                        v410KubeApiserverCheckEndpointsKubeconfigCmYaml,
	"v4.1.0/kube-apiserver/check-endpoints-rolebinding-kube-system.yaml":              v410KubeApiserverCheckEndpointsRolebindingKubeSystemYaml,
	"v4.1.0/kube-apiserver/check-endpoints-rolebinding.yaml":                          v410KubeApiserverCheckEndpointsRolebindingYaml,
	"v4.1.0/kube-apiserver/cm.yaml":                                                   v410KubeApiserverCmYaml,
	"v4.1.0/kube-apiserver/control-plane-node-kubeconfig-cm.yaml":                     v410KubeApiserverControlPlaneNodeKubeconfigCmYaml,
	"v4.1.0/kube-apiserver/delegated-incluster-authentication-rolebinding.yaml":       v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYaml,
	"v4.1.0/kube-apiserver/kubeconfig-cm.yaml":                                        v410KubeApiserverKubeconfigCmYaml,
	"v4.1.0/kube-apiserver/localhost-recovery-client-crb.yaml":                        v410KubeApiserverLocalhostRecoveryClientCrbYaml,
	"v4.1.0/kube-apiserver/localhost-recovery-sa.yaml":                                v410KubeApiserverLocalhostRecoverySaYaml,
	"v4.1.0/kube-apiserver/localhost-recovery-token.yaml":                             v410KubeApiserverLocalhostRecoveryTokenYaml,
	"v4.1.0/kube-apiserver/node-kubeconfigs.yaml":                                     v410KubeApiserverNodeKubeconfigsYaml,
	"v4.1.0/kube-apiserver/ns.yaml":                                                   v410KubeApiserverNsYaml,
	"v4.1.0/kube-apiserver/pod-cm.yaml":                                               v410KubeApiserverPodCmYaml,
	"v4.1.0/kube-apiserver/pod.yaml":                                                  v410KubeApiserverPodYaml,
	"v4.1.0/kube-apiserver/recovery-config.yaml":                                      v410KubeApiserverRecoveryConfigYaml,
	"v4.1.0/kube-apiserver/recovery-encryption-config.yaml":                           v410KubeApiserverRecoveryEncryptionConfigYaml,
	"v4.1.0/kube-apiserver/recovery-pod.yaml":                                         v410KubeApiserverRecoveryPodYaml,
	"v4.1.0/kube-apiserver/rollout-monitor-pod-cm.yaml":                               v410KubeApiserverRolloutMonitorPodCmYaml,
	"v4.1.0/kube-apiserver/rollout-monitor-pod.yaml":                                  v410KubeApiserverRolloutMonitorPodYaml,
	"v4.1.0/kube-apiserver/storage-version-migration-flowschema.yaml":                 v410KubeApiserverStorageVersionMigrationFlowschemaYaml,
	"v4.1.0/kube-apiserver/storage-version-migration-prioritylevelconfiguration.yaml": v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYaml,
	"v4.1.0/kube-apiserver/svc.yaml":                                                  v410KubeApiserverSvcYaml,
	"v4.1.0/kube-apiserver/trusted-ca-cm.yaml":                                        v410KubeApiserverTrustedCaCmYaml,
}

// AssetDir returns the file names below a certain
// directory embedded in the file by go-bindata.
// For example if you run go-bindata on data/... and data contains the
// following hierarchy:
//     data/
//       foo.txt
//       img/
//         a.png
//         b.png
// then AssetDir("data") would return []string{"foo.txt", "img"}
// AssetDir("data/img") would return []string{"a.png", "b.png"}
// AssetDir("foo.txt") and AssetDir("notexist") would return an error
// AssetDir("") will return []string{"data"}.
func AssetDir(name string) ([]string, error) {
	node := _bintree
	if len(name) != 0 {
		cannonicalName := strings.Replace(name, "\\", "/", -1)
		pathList := strings.Split(cannonicalName, "/")
		for _, p := range pathList {
			node = node.Children[p]
			if node == nil {
				return nil, fmt.Errorf("Asset %s not found", name)
			}
		}
	}
	if node.Func != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	rv := make([]string, 0, len(node.Children))
	for childName := range node.Children {
		rv = append(rv, childName)
	}
	return rv, nil
}

type bintree struct {
	Func     func() (*asset, error)
	Children map[string]*bintree
}

var _bintree = &bintree{nil, map[string]*bintree{
	"v4.1.0": {nil, map[string]*bintree{
		"alerts": {nil, map[string]*bintree{
			"api-usage.yaml":               {v410AlertsApiUsageYaml, map[string]*bintree{}},
			"cpu-utilization.yaml":         {v410AlertsCpuUtilizationYaml, map[string]*bintree{}},
			"kube-apiserver-requests.yaml": {v410AlertsKubeApiserverRequestsYaml, map[string]*bintree{}},
			"kube-apiserver-slos.yaml":     {v410AlertsKubeApiserverSlosYaml, map[string]*bintree{}},
		}},
		"config": {nil, map[string]*bintree{
			"config-overrides.yaml": {v410ConfigConfigOverridesYaml, map[string]*bintree{}},
			"defaultconfig.yaml":    {v410ConfigDefaultconfigYaml, map[string]*bintree{}},
		}},
		"kube-apiserver": {nil, map[string]*bintree{
			"apiserver.openshift.io_apirequestcount.yaml":               {v410KubeApiserverApiserverOpenshiftIo_apirequestcountYaml, map[string]*bintree{}},
			"check-endpoints-clusterrole-crd-reader.yaml":               {v410KubeApiserverCheckEndpointsClusterroleCrdReaderYaml, map[string]*bintree{}},
			"check-endpoints-clusterrole-node-reader.yaml":              {v410KubeApiserverCheckEndpointsClusterroleNodeReaderYaml, map[string]*bintree{}},
			"check-endpoints-clusterrole.yaml":                          {v410KubeApiserverCheckEndpointsClusterroleYaml, map[string]*bintree{}},
			"check-endpoints-clusterrolebinding-auth-delegator.yaml":    {v410KubeApiserverCheckEndpointsClusterrolebindingAuthDelegatorYaml, map[string]*bintree{}},
			"check-endpoints-clusterrolebinding-crd-reader.yaml":        {v410KubeApiserverCheckEndpointsClusterrolebindingCrdReaderYaml, map[string]*bintree{}},
			"check-endpoints-clusterrolebinding-node-reader.yaml":       {v410KubeApiserverCheckEndpointsClusterrolebindingNodeReaderYaml, map[string]*bintree{}},
			"check-endpoints-kubeconfig-cm.yaml":                        {v410KubeApiserverCheckEndpointsKubeconfigCmYaml, map[string]*bintree{}},
			"check-endpoints-rolebinding-kube-system.yaml":              {v410KubeApiserverCheckEndpointsRolebindingKubeSystemYaml, map[string]*bintree{}},
			"check-endpoints-rolebinding.yaml":                          {v410KubeApiserverCheckEndpointsRolebindingYaml, map[string]*bintree{}},
			"cm.yaml":                                                   {v410KubeApiserverCmYaml, map[string]*bintree{}},
			"control-plane-node-kubeconfig-cm.yaml":                     {v410KubeApiserverControlPlaneNodeKubeconfigCmYaml, map[string]*bintree{}},
			"delegated-incluster-authentication-rolebinding.yaml":       {v410KubeApiserverDelegatedInclusterAuthenticationRolebindingYaml, map[string]*bintree{}},
			"kubeconfig-cm.yaml":                                        {v410KubeApiserverKubeconfigCmYaml, map[string]*bintree{}},
			"localhost-recovery-client-crb.yaml":                        {v410KubeApiserverLocalhostRecoveryClientCrbYaml, map[string]*bintree{}},
			"localhost-recovery-sa.yaml":                                {v410KubeApiserverLocalhostRecoverySaYaml, map[string]*bintree{}},
			"localhost-recovery-token.yaml":                             {v410KubeApiserverLocalhostRecoveryTokenYaml, map[string]*bintree{}},
			"node-kubeconfigs.yaml":                                     {v410KubeApiserverNodeKubeconfigsYaml, map[string]*bintree{}},
			"ns.yaml":                                                   {v410KubeApiserverNsYaml, map[string]*bintree{}},
			"pod-cm.yaml":                                               {v410KubeApiserverPodCmYaml, map[string]*bintree{}},
			"pod.yaml":                                                  {v410KubeApiserverPodYaml, map[string]*bintree{}},
			"recovery-config.yaml":                                      {v410KubeApiserverRecoveryConfigYaml, map[string]*bintree{}},
			"recovery-encryption-config.yaml":                           {v410KubeApiserverRecoveryEncryptionConfigYaml, map[string]*bintree{}},
			"recovery-pod.yaml":                                         {v410KubeApiserverRecoveryPodYaml, map[string]*bintree{}},
			"rollout-monitor-pod-cm.yaml":                               {v410KubeApiserverRolloutMonitorPodCmYaml, map[string]*bintree{}},
			"rollout-monitor-pod.yaml":                                  {v410KubeApiserverRolloutMonitorPodYaml, map[string]*bintree{}},
			"storage-version-migration-flowschema.yaml":                 {v410KubeApiserverStorageVersionMigrationFlowschemaYaml, map[string]*bintree{}},
			"storage-version-migration-prioritylevelconfiguration.yaml": {v410KubeApiserverStorageVersionMigrationPrioritylevelconfigurationYaml, map[string]*bintree{}},
			"svc.yaml":           {v410KubeApiserverSvcYaml, map[string]*bintree{}},
			"trusted-ca-cm.yaml": {v410KubeApiserverTrustedCaCmYaml, map[string]*bintree{}},
		}},
	}},
}}

// RestoreAsset restores an asset under the given directory
func RestoreAsset(dir, name string) error {
	data, err := Asset(name)
	if err != nil {
		return err
	}
	info, err := AssetInfo(name)
	if err != nil {
		return err
	}
	err = os.MkdirAll(_filePath(dir, filepath.Dir(name)), os.FileMode(0755))
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(_filePath(dir, name), data, info.Mode())
	if err != nil {
		return err
	}
	err = os.Chtimes(_filePath(dir, name), info.ModTime(), info.ModTime())
	if err != nil {
		return err
	}
	return nil
}

// RestoreAssets restores an asset under the given directory recursively
func RestoreAssets(dir, name string) error {
	children, err := AssetDir(name)
	// File
	if err != nil {
		return RestoreAsset(dir, name)
	}
	// Dir
	for _, child := range children {
		err = RestoreAssets(dir, filepath.Join(name, child))
		if err != nil {
			return err
		}
	}
	return nil
}

func _filePath(dir, name string) string {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	return filepath.Join(append([]string{dir}, strings.Split(cannonicalName, "/")...)...)
}
