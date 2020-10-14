module github.com/openshift/cluster-kube-apiserver-operator

go 1.13

require (
	github.com/apparentlymart/go-cidr v1.0.1
	github.com/blang/semver v3.5.0+incompatible
	github.com/certifi/gocertifi v0.0.0-20190905060710-a5e0173ced67 // indirect
	github.com/davecgh/go-spew v1.1.1
	github.com/getsentry/raven-go v0.2.0 // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/gonum/graph v0.0.0-20190426092945-678096d81a4b
	github.com/google/go-cmp v0.4.0
	github.com/imdario/mergo v0.3.8
	github.com/openshift/api v0.0.0-20200929165121-b7210d15c07d
	github.com/openshift/build-machinery-go v0.0.0-20200819073603-48aa266c95f7
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/library-go v0.0.0-20200930152912-7219e4f70a45
	github.com/pkg/profile v1.5.0 // indirect
	github.com/prometheus/client_golang v1.7.1
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	go.etcd.io/etcd v0.5.0-alpha.5.0.20200819165624-17cef6e3e9d5
	k8s.io/api v0.19.0
	k8s.io/apiextensions-apiserver v0.19.0
	k8s.io/apimachinery v0.19.0
	k8s.io/apiserver v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/component-base v0.19.0
	k8s.io/klog/v2 v2.3.0
	k8s.io/utils v0.0.0-20200729134348-d5654de09c73
	sigs.k8s.io/kube-storage-version-migrator v0.0.3
)
