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

* `host-etcd` *endpoints* in *kube-system* namespace
  - The observed endpoint addresses are used to configure the `storageConfig.urls` in Kubernetes API server configuration.
* `cluster` *image.config.openshift.io* custom resource
  - The observed CR resource is used to configure the `imagePolicyConfig.internalRegistryHostname` in Kubernetes API server configuration
* `cluster-config-v1` *configmap* in *kube-system* namespace
  - The observed configmap `install-config` is decoded and the `networking.podCIDR` and `networking.serviceCIDR` is extracted and used as input for `admissionPluginConfig.openshift.io/RestrictedEndpointsAdmission.configuration.restrictedCIDRs` and `servicesSubnet`


The configuration for the Kubernetes API server is the result of merging:

* a [default config](https://github.com/openshift/cluster-kube-apiserver-operator/blob/master/bindata/assets/config/defaultconfig.yaml)
* observed config (compare observed values above) `spec.spec.unsupportedConfigOverrides` from the `kubeapiserveroperatorconfig`.

All of these are sparse configurations, i.e. unvalidated json snippets which are merged in order to form a valid configuration at the end.


## Debugging

Operator also expose events that can help debugging issues. To get operator events, run following command:

```
$ oc get events -n  openshift-cluster-kube-apiserver-operator
```

This operator is configured via [`KubeAPIServer`](https://github.com/openshift/api/blob/master/operator/v1/types_kubeapiserver.go#L12) custom resource:

```
$ oc describe kubeapiserver
```
```yaml
apiVersion: operator.openshift.io/v1
kind: KubeAPIServer
metadata:
  name: cluster
spec:
  managementState: Managed
```

The current operator status is reported using the `ClusterOperator` resource. To get the current status you can run follow command:

```
$ oc get clusteroperator/kube-apiserver
```

## Developing and debugging the operator

In the running cluster [cluster-version-operator](https://github.com/openshift/cluster-version-operator/) is responsible
for maintaining functioning and non-altered elements.  In that case to be able to use custom operator image one has to
perform one of these operations:

1. Set your operator in umanaged state, see [here](https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/dev/clusterversion.md) for details, in short:

```
oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps
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

## Debugging with delve

1. In order to debug a running container remotely the image needs to contain source code and the binary must have debugging symbols enabled:
```
FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.20-openshift-4.15 AS builder
ENV GO_PACKAGE github.com/openshift/cluster-kube-apiserver-operator
ENV GO_BUILD_FLAGS="-gcflags=all='-N -l'"
RUN make build --warn-undefined-variables

FROM registry.ci.openshift.org/ocp/4.15:base
COPY . /source
...
```


2. Run this to allow privileged pods in the namespace:
```
oc label ns/openshift-kube-apiserver-operator pod-security.kubernetes.io/enforce=privileged pod-security.kubernetes.io/audit=privileged pod-security.kubernetes.io/warn=privileged --overwrite
```

3. Stop CVO from reverting changes to deployment:
```
oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps
    kind: Deployment
    name: kube-apiserver-operator
    namespace: openshift-kube-apiserver-operator
    unmanaged: true
EOF
)"
```

4. Update permissions for cluster-kube-apiserver-operator SA:
```
oc policy add-role-to-user cluster-admin -z kube-apiserver-operator -n openshift-kube-apiserver-operator
```

5. Patch deployment to make main container copy source from the image to emptyDir for delve:
```
oc -n openshift-kube-apiserver-operator patch deployment/kube-apiserver-operator -p "$(cat <<- EOF
spec:
  template:
    spec:
      volumes:
      - name: src
        emptyDir: {}
      containers:
      - name: kube-apiserver-operator
        volumeMounts:
          - name: src
            mountPath: /go
        lifecycle:
          postStart:
            exec:
              command:
                - /bin/bash
                - '-c'
                - mkdir -p /go/src/github.com/openshift/cluster-kube-apiserver-operator && cp -r /source/* /go/src/github.com/openshift/cluster-kube-apiserver-operator
```

6. Patch deployment to run delve as sidecar:
```
oc -n openshift-kube-apiserver-operator patch deployment/kube-apiserver-operator -p "$(cat <<- EOF
spec:
  template:
    spec:
      shareProcessNamespace: true
      containers:
      - name: delve
        securityContext:
          privileged: true
          runAsUser: 0
          capabilities:
            add: ["SYS_PTRACE"]
        image: quay.io/vrutkovs/ocp:delve
        ports:
        - containerPort: 40000
          name: delve
          protocol: TCP
        volumeMounts:
          - name: src
            mountPath: /go
        command:
        - sh
        - -c
        - "echo \"Waiting for cluster-kube-apiserver-operator\" && until [ -n \"\${PID}\" ]; do echo -n \".\" && sleep 1 && export PID=\$(pidof cluster-kube-apiserver-operator); done && echo \"Found operator with PID \${PID}\" && /usr/local/bin/dlv --listen=:40000 --headless=true --api-version=2 --accept-multiclient=true --allow-non-terminal-interactive=true --continue --log attach \${PID}"
EOF
)"
```
7. Patch deployment to remove non-root restriction:
```
oc -n openshift-kube-apiserver-operator patch deployment/kube-apiserver-operator --type json -p='[{"op": "remove", "path": "/spec/template/spec/securityContext/runAsNonRoot"}]'
```

8. Patch deployment to point to the new image with debugging symbols and source code:
```
oc -n openshift-kube-apiserver-operator patch deployment/kube-apiserver-operator --type json -p='[{"op": "replace", "path": "/spec/template/spec/containers/1/image", "value": "quay.io/vrutkovs/ocp:ckao-debugging-v3"}]'
```

9. Port forward remote port 40000 locally:
```
oc -n openshift-kube-apiserver-operator port-forward deployment/kube-apiserver-operator 40000:40000
```

10. Run `dlv attach :40000` or use VSCode/GoLand debugger in attach mode (remotePath needs to be set to `/go/src/github.com/openshift/cluster-kube-apiserver-operator`)

## Developing and debugging the bootkube bootstrap phase

The operator image version used by the [https://github.com/openshift/installer/blob/master/pkg/asset/ignition/bootstrap/bootstrap.go#L178](installer) bootstrap phase can be overridden by creating a custom origin-release image pointing to the developer's operator `:latest` image:

```
$ IMAGE_ORG=sttts make images
$ docker push sttts/origin-cluster-kube-apiserver-operator

$ cd ../cluster-kube-apiserver-operator
$ oc adm release new --from-release=registry.svc.ci.openshift.org/openshift/origin-release:v4.0 cluster-kube-apiserver-operator=docker.io/sttts/origin-cluster-kube-apiserver-operator:latest --to-image=sttts/origin-release:latest

$ cd ../installer
$ OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE=docker.io/sttts/origin-release:latest bin/openshift-install cluster ...
```
