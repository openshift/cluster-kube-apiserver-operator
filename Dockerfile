# The standard name for this image is openshift/origin-cluster-kube-apiserver-operator
#
FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-kube-apiserver-operator
RUN cd /go/src/github.com/openshift/cluster-kube-apiserver-operator && go build ./cmd/cluster-kube-apiserver-operator

FROM centos:7
RUN mkdir -p /usr/share/bootkube/manifests
COPY --from=0 /go/src/github.com/openshift/cluster-kube-apiserver-operator/bindata/bootkube/* /usr/share/bootkube/manifests/
COPY --from=0 /go/src/github.com/openshift/cluster-kube-apiserver-operator/cluster-kube-apiserver-operator /usr/bin/cluster-kube-apiserver-operator

COPY manifests /manifests
LABEL io.openshift.release.operator true
