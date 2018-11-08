# The standard name for this image is openshift/origin-cluster-kube-apiserver-operator
#
FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-kube-apiserver-operator
WORKDIR /go/src/github.com/openshift/cluster-kube-apiserver-operator
ENV GO_PACKAGE github.com/openshift/cluster-kube-apiserver-operator
RUN go build -ldflags "-X $GO_PACKAGE/pkg/version.versionFromGit=$(git describe --long --tags --abbrev=7 --match 'v[0-9]*')" ./cmd/cluster-kube-apiserver-operator

FROM centos:7
RUN mkdir -p /usr/share/bootkube/manifests
COPY --from=0 /go/src/github.com/openshift/cluster-kube-apiserver-operator/bindata/bootkube/* /usr/share/bootkube/manifests/
COPY --from=0 /go/src/github.com/openshift/cluster-kube-apiserver-operator/cluster-kube-apiserver-operator /usr/bin/cluster-kube-apiserver-operator

COPY manifests /manifests
LABEL io.openshift.release.operator true
