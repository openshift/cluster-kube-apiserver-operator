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
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/imdario/mergo v0.3.8
	github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/kubernetes-sigs/kube-storage-version-migrator v0.0.0-20191127225502-51849bc15f17
	github.com/openshift/api v0.0.0-20200323095748-e7041f8762a3
	github.com/openshift/build-machinery-go v0.0.0-20200211121458-5e3d6e570160
	github.com/openshift/client-go v0.0.0-20200320150128-a906f3d8e723
	github.com/openshift/library-go v0.0.0-20200320155611-2a351bebf158
	github.com/prometheus/client_golang v1.1.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.18.0-beta.2
	k8s.io/apimachinery v0.18.0-beta.2
	k8s.io/apiserver v0.18.0-beta.2
	k8s.io/client-go v0.18.0-beta.2
	k8s.io/component-base v0.18.0-beta.2
	k8s.io/klog v1.0.0
)

replace github.com/jteeuwen/go-bindata => github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
