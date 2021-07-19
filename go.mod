module github.com/openshift/cluster-kube-apiserver-operator

go 1.16

require (
	github.com/apparentlymart/go-cidr v1.0.1
	github.com/blang/semver v3.5.1+incompatible
	github.com/davecgh/go-spew v1.1.1
	github.com/ghodss/yaml v1.0.0
	github.com/gonum/graph v0.0.0-20190426092945-678096d81a4b
	github.com/google/go-cmp v0.5.5
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/imdario/mergo v0.3.8
	github.com/openshift/api v0.0.0-20210706092853-b63d499a70ce
	github.com/openshift/build-machinery-go v0.0.0-20210712174854-1bb7fd1518d3
	github.com/openshift/client-go v0.0.0-20210521082421-73d9475a9142
	github.com/openshift/library-go v0.0.0-20210715155611-70a39c8ba7a1
	github.com/pkg/profile v1.5.0 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/client v0.45.0
	github.com/prometheus/client_golang v1.11.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	go.etcd.io/etcd/client/v3 v3.5.0
	golang.org/x/sys v0.0.0-20210616094352-59db8d763f22
	k8s.io/api v0.22.0-beta.2
	k8s.io/apiextensions-apiserver v0.22.0-beta.2
	k8s.io/apimachinery v0.22.0-beta.2
	k8s.io/apiserver v0.22.0-beta.2
	k8s.io/client-go v0.22.0-beta.2
	k8s.io/component-base v0.22.0-beta.2
	k8s.io/klog/v2 v2.9.0
	k8s.io/utils v0.0.0-20210707171843-4b05e18ac7d9
	sigs.k8s.io/kube-storage-version-migrator v0.0.4
)

replace (
	github.com/openshift/api => github.com/soltysh/api v0.0.0-20210719081803-9091ab00c164
	github.com/openshift/client-go => github.com/soltysh/client-go v0.0.0-20210719082425-f8fde3619384
	github.com/openshift/library-go => github.com/soltysh/library-go v0.0.0-20210719104342-c952f4e07d0b
)
