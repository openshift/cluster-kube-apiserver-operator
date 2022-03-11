local kasAlerts = import 'github.com/kubernetes-monitoring/kubernetes-mixin/alerts/kube_apiserver.libsonnet';
local mixinConfig = import 'github.com/kubernetes-monitoring/kubernetes-mixin/config.libsonnet';
local kasRules = import 'github.com/kubernetes-monitoring/kubernetes-mixin/rules/kube_apiserver.libsonnet';

local sloGroups = ['kube-apiserver-slos', 'kube-apiserver-burnrate.rules'];

{
  mixin:: mixinConfig + kasAlerts + kasRules,

  'kube-apiserver-slos': {
    apiVersion: 'monitoring.coreos.com/v1',
    kind: 'PrometheusRule',
    metadata: {
      name: 'kubernetes-monitoring-rules',
      namespace: 'openshift-kube-apiserver-operator',
    },
    spec: {
      local r = if std.objectHasAll($.mixin, 'prometheusRules') then $.mixin.prometheusRules.groups else [],
      local a = if std.objectHasAll($.mixin, 'prometheusAlerts') then $.mixin.prometheusAlerts.groups else [],
      groups: std.filter(
        function(g) std.member(sloGroups, g.name),
        a + r
      ),
    },
  },
}
