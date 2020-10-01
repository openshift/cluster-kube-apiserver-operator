module github.com/openshift/cluster-kube-apiserver-operator

go 1.13

require (
	github.com/apparentlymart/go-cidr v1.0.1
	github.com/blang/semver v3.5.0+incompatible
	github.com/certifi/gocertifi v0.0.0-20190905060710-a5e0173ced67 // indirect
	github.com/coreos/etcd v3.3.15+incompatible
	github.com/davecgh/go-spew v1.1.1
	github.com/getsentry/raven-go v0.2.0 // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/gonum/graph v0.0.0-20190426092945-678096d81a4b
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/imdario/mergo v0.3.8
	github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/kubernetes-sigs/kube-storage-version-migrator v0.0.0-20191127225502-51849bc15f17
	github.com/openshift/api v0.0.0-20200618202633-7192180f496a
	github.com/openshift/build-machinery-go v0.0.0-20200211121458-5e3d6e570160
	github.com/openshift/client-go v0.0.0-20200116152001-92a2713fa240
	github.com/openshift/library-go v0.0.0-20200921144613-67f7770bf823
	github.com/prometheus/client_golang v1.1.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/apiserver v0.17.3
	k8s.io/client-go v0.17.3
	k8s.io/component-base v0.17.3
	k8s.io/klog v1.0.0
)

replace (
	github.com/jteeuwen/go-bindata => github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible

	// upstream kube-storage-version-migrator forces these to require a replace
	k8s.io/api => k8s.io/api v0.17.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.3
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.3
	// use kaso-4.4-kubernetes-1.17.3 with patches for SNI
	k8s.io/client-go => github.com/openshift/kubernetes-client-go v0.0.0-20200304132934-02649b23d68a
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.17.3
)
