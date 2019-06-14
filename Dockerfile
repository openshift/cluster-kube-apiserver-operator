FROM registry.svc.ci.openshift.org/openshift/release:golang-1.12 AS builder
WORKDIR /go/src/github.com/openshift/cluster-kube-apiserver-operator
COPY . .
ENV GO_PACKAGE github.com/openshift/cluster-kube-apiserver-operator
RUN go build -ldflags "-X $GO_PACKAGE/pkg/version.versionFromGit=$(git describe --long --tags --abbrev=7 --match 'v[0-9]*')" ./cmd/cluster-kube-apiserver-operator

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
RUN mkdir -p /usr/share/bootkube/manifests
COPY --from=builder /go/src/github.com/openshift/cluster-kube-apiserver-operator/bindata/bootkube/* /usr/share/bootkube/manifests/
COPY --from=builder /go/src/github.com/openshift/cluster-kube-apiserver-operator/cluster-kube-apiserver-operator /usr/bin/
COPY manifests/*.yaml /manifests
COPY manifests/image-references /manifests
LABEL io.openshift.release.operator true
# FIXME: entrypoint shouldn't be bash but the binary (needs fixing the chain)
# ENTRYPOINT ["/usr/bin/cluster-kube-apiserver-operator"]

