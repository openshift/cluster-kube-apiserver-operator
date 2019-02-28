# Kubernetes API Server Operator

The Kubernetes API Server operator manages and updates the [Kubernetes API server](https://github.com/kubernetes/kubernetes) deployed on top of
[OpenShift](https://openshift.io). The operator is based on OpenShift [library-go](https://github.com/openshift/library-go) framework and it
 is installed via [Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

It contains the following components:

* Operator
* Bootstrap manifest renderer
* Installer based on static pods
* Configuration observer

By default, the operator exposes [Prometheus](https://prometheus.io) metrics via `metrics` service.
The metrics are collected from following components:

* Kubernetes API Server Operator


## Configuration

The configuration observer component is responsible for reacting on external configuration changes.
For example, this allows external components ([registry](https://github.com/openshift/cluster-image-registry-operator), etcd, etc..)
to interact with the Kubernetes API server configuration ([KubeAPIServerConfig](https://github.com/openshift/api/blob/master/kubecontrolplane/v1/types.go#L14) custom resource).

Currently changes in following external components are being observed:

* `cluster` *authentications.config.openshift.io* custom resource
  - Configures `authConfig.oauthMetadataFile`.
* `cluster` *apiservers.config.openshift.io* custom resource
  - Adds user defined client CA bundle.
  - Configures `servingInfo.certFile` and `servingInfo.keyFile`.
  - Configures `servingInfo.namedCertificates`.
* `cluster` *networks.config.openshift.io* custom resource
  - Configures `admissionPluginConfig.openshift.io/RestrictedEndpointsAdmission.configuration.restrictedCIDRs`.
  - Configures `servicesSubnet`.
* `cluster` *images.config.openshift.io* custom resource
  - Configures `imagePolicyConfig.internalRegistryHostname`.
* `initial-sa-token-signing-certs` *configmap* in *openshift-kube-apiserver* namespace
  - Configures `serviceAccountPublicKeyFiles`.
* `kube-controller-manager-sa-token-signing-certs` *configmap* in *openshift-kube-apiserver* namespace
  - Configures `serviceAccountPublicKeyFiles`.
* `host-etcd` *endpoints* in *kube-system* namespace
  - Configures `storageConfig.urls`.

The configuration for the Kubernetes API server is the result of merging:

* a [default config](https://github.com/openshift/cluster-kube-apiserver-operator/blob/master/bindata/v4.1.0/config/defaultconfig.yaml)
* observed config (compare observed values above)
* `spec.spec.unsupportedConfigOverrides` from the `kubeapiserver`.

All of these are sparse configurations, i.e. unvalidated json snippets which are merged in order to form a valid configuration at the end.


## Debugging

Operator also expose events that can help debugging issues. To get operator events, run following command:

```
$ oc get events -n  openshift-cluster-kube-apiserver-operator
```

This operator is configured via [`KubeAPIServer`](https://github.com/openshift/api/blob/master/operator/v1/types_kubeapiserver.go) custom resource:

```console
$ oc describe kubeapiserver
Name:         cluster
API Version:  operator.openshift.io/v1
Kind:         KubeAPIServer
Spec:
  Management State:           Managed
```

The current operator status is reported using the `ClusterOperator` resource. To get the current status you can run follow command:

```
$ oc get clusteroperator/kube-apiserver
```

## Developing and debugging the operator

In the running cluster [cluster-version-operator](https://github.com/openshift/cluster-version-operator/) is responsible
for maintaining functioning and non-altered elements.  In that case to be able to use custom operator image one has to
perform one of these operations:

1. Set your operator in umanaged state, see [here](https://github.com/openshift/cluster-version-operator/blob/master/docs/dev/clusterversion.md) for details, in short:

```
oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps/v1
    kind: Deployment
    name: kube-apiserver-operator
    namespace: openshift-kube-apiserver-operator
    unmanaged: true
EOF
)"
```

2. Scale down cluster-version-operator:

```
oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version
```

IMPORTANT: This approach disables cluster-version-operator completely, whereas the previous patch only tells it to not manage a kube-apiserver-operator!

After doing this you can now change the image of the operator to the desired one:

```
oc patch pod/kube-apiserver-operator-<rand_digits> -n openshift-kube-apiserver-operator -p '{"spec":{"containers":[{"name":"kube-apiserver-operator","image":"<user>/cluster-kube-apiserver-operator"}]}}'
```


## Developing and debugging the bootkube bootstrap phase

The operator image version used by the installer bootstrap phase can be overridden by creating a custom origin-release 
image pointing to the developer's operator `:latest` image:

```
$ IMAGE_ORG=sttts make images
$ docker push sttts/origin-cluster-kube-apiserver-operator

$ cd ../cluster-kube-apiserver-operator
$ oc adm release new --from-release=registry.svc.ci.openshift.org/openshift/origin-release:v4.0 cluster-kube-apiserver-operator=docker.io/sttts/origin-cluster-kube-apiserver-operator:latest --to-image=sttts/origin-release:latest

$ cd ../installer
$ OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE=docker.io/sttts/origin-release:latest bin/openshift-install cluster ...
```
