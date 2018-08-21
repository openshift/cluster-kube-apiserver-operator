#
# This is the integrated OpenShift Service Serving Cert Signer.  It signs serving certificates for use inside the platform.
#
# The standard name for this image is openshift/origin-cluster-kube-apiserver-operator
#
FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-kube-apiserver-operator
RUN cd /go/src/github.com/openshift/cluster-kube-apiserver-operator && go build ./cmd/cluster-kube-apiserver-operator

FROM centos:7
COPY --from=0 /go/src/github.com/openshift/cluster-kube-apiserver-operator/cluster-kube-apiserver-operator /usr/bin/cluster-kube-apiserver-operator
