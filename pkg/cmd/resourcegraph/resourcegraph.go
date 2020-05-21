package resourcegraph

import (
	"fmt"

	"github.com/gonum/graph/encoding/dot"
	"github.com/spf13/cobra"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/resource/resourcegraph"
)

func NewResourceChainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource-graph",
		Short: "Provides an often out-dated snapshot of where resources come from.",
		Run: func(cmd *cobra.Command, args []string) {
			resources := Resources()
			g := resources.NewGraph()

			data, err := dot.Marshal(g, resourcegraph.Quote("kube-apiserver-operator"), "", "  ", false)
			if err != nil {
				klog.Fatal(err)
			}
			fmt.Println(string(data))
		},
	}

	return cmd
}

func Resources() resourcegraph.Resources {
	ret := resourcegraph.NewResources()

	payload := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "Payload", "", "cluster")).
		Add(ret)
	installer := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "Installer", "", "cluster")).
		Add(ret)
	user := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "User", "", "cluster")).
		Add(ret)

	cvo := resourcegraph.NewOperator("cluster-version").
		From(payload).
		Add(ret)
	kasOperator := resourcegraph.NewOperator("kube-apiserver").
		From(cvo).
		Add(ret)
	kcmOperator := resourcegraph.NewOperator("kube-controller-manager").
		From(cvo).
		Add(ret)
	authenticationOperator := resourcegraph.NewOperator("authentication").
		From(cvo).
		Add(ret)
	imageRegistryOperator := resourcegraph.NewOperator("image-registry").
		From(cvo).
		Add(ret)
	networkOperator := resourcegraph.NewOperator("network").
		From(cvo).
		Add(ret)

	// config.openshift.io
	apiserverConfig := resourcegraph.NewConfig("apiservers").
		From(user).
		Add(ret)
	userClientCA := resourcegraph.NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-client-ca>").
		Note("User").
		From(user).
		From(apiserverConfig).
		Add(ret)
	userDefaultServing := resourcegraph.NewSecret(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-default-serving>").
		Note("User").
		From(user).
		From(apiserverConfig).
		Add(ret)
	authenticationConfig := resourcegraph.NewConfig("authentications").
		From(user).
		From(authenticationOperator).
		Add(ret)
	userWellKnown := resourcegraph.NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "<user-specified-well-known>").
		Note("User").
		From(user).
		From(authenticationConfig).
		Add(ret)
	managedWellKnown := resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "openshift-authentication").
		Note("Managed").
		From(authenticationOperator).
		From(authenticationConfig).
		Add(ret)
	imageConfig := resourcegraph.NewConfig("images").
		From(user).
		From(imageRegistryOperator).
		Add(ret)
	networkConfig := resourcegraph.NewConfig("network").
		From(user).
		From(networkOperator).
		Add(ret)
	infrastructureConfig := resourcegraph.NewConfig("infrastructure").
		From(user).
		From(installer).
		Add(ret)

	// aggregator client
	aggregatorSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "aggregator-client-signer").
		Note("Rotated").
		From(kasOperator).
		From(installer).
		Add(ret)
	aggregatorClient := resourcegraph.NewSecret(operatorclient.TargetNamespace, "aggregator-client").
		Note("Rotated").
		From(aggregatorSigner).
		Add(ret)
	// this is a destination and consumed by OAS
	operatorManagedAggregatorClientCA := resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-aggregator-client-ca").
		Note("Rotated").
		From(aggregatorSigner).
		Add(ret)
	kasAggregatorClientCAForPod := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "aggregator-client-ca").
		Note("Synchronized").
		From(operatorManagedAggregatorClientCA).
		Add(ret)

	// client CAs
	adminKubeconfigCA := resourcegraph.NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "admin-kubeconfig-client-ca").
		Note("Static").
		From(installer).
		Add(ret)
	kcmControllerCSRCA := resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "csr-controller-ca").
		Note("Synchronized").
		From(kcmOperator).
		From(installer).
		Add(ret)
	kasToKubeletSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "kube-apiserver-to-kubelet-signer").
		Note("Rotated").
		From(installer).
		Add(ret)
	kasToKubeletCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "kube-apiserver-to-kubelet-client-ca").
		Note("Rotated").
		From(kasToKubeletSigner).
		Add(ret)
	kubeControlPlaneSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "kube-control-plane-signer").
		Note("Rotated").
		From(installer).
		Add(ret)
	_ = resourcegraph.NewSecret(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-controller-manager-client-cert-key").
		Note("Rotated").
		From(kubeControlPlaneSigner).
		Add(ret)
	_ = resourcegraph.NewSecret(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-scheduler-client-cert-key").
		Note("Rotated").
		From(kubeControlPlaneSigner).
		Add(ret)
	kubeControlPlaneCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "kube-control-plane-signer-ca").
		Note("Rotated").
		From(kubeControlPlaneSigner).
		Add(ret)
	clientCA := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "client-ca").
		Note("Unioned").
		From(adminKubeconfigCA).
		From(kcmControllerCSRCA).
		From(kasToKubeletCA).
		From(kubeControlPlaneCA).
		From(userClientCA).
		Add(ret)
	// this is a destination and consumed by OAS
	_ = resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-client-ca").
		Note("Synchronized").
		From(clientCA).
		Add(ret)

	// etcd certs
	fromEtcdServingCA := resourcegraph.NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "etcd-serving-ca").
		Note("Static").
		From(installer).
		Add(ret)
	fromEtcdClient := resourcegraph.NewSecret(operatorclient.GlobalUserSpecifiedConfigNamespace, "etcd-client").
		Note("Static").
		From(installer).
		Add(ret)
	etcdServingCA := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "etcd-serving-ca").
		Note("Synchronized").
		From(fromEtcdServingCA).
		Add(ret)
	etcdClient := resourcegraph.NewSecret(operatorclient.TargetNamespace, "etcd-client").
		Note("Synchronized").
		From(fromEtcdClient).
		Add(ret)

	// kubelet client
	kubeletClient := resourcegraph.NewSecret(operatorclient.TargetNamespace, "kubelet-client").
		Note("Rotated").
		From(kasToKubeletSigner).
		Add(ret)

	// kubelet serving
	// TODO this is just a courtesy for the pod team
	kubeletServingCA := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "kubelet-serving-ca").
		Note("Synchronized").
		From(kcmControllerCSRCA).
		Add(ret)
	// this is a destination for things like monitoring to get a kubelet serving CA
	_ = resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kubelet-serving-ca").
		Note("Synchronized").
		From(kubeletServingCA).
		Add(ret)

	// sa token verification
	kcmSATokenPub := resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "sa-token-signing-certs").
		Note("Static").
		From(kcmOperator).
		From(installer).
		Add(ret)

	// serving
	loadBalancerSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "loadbalancer-serving-signer").
		Note("Rotated").
		From(installer).
		Add(ret)
	loadBalancerCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "loadbalancer-serving-ca").
		Note("Rotated").
		From(loadBalancerSigner).
		Add(ret)
	internalLBServing := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "internal-loadbalancer-serving-certkey").
		Note("Rotated").
		From(loadBalancerSigner).
		From(infrastructureConfig).
		Add(ret)
	externalLBServing := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "external-loadbalancer-serving-certkey").
		Note("Rotated").
		From(loadBalancerSigner).
		From(infrastructureConfig).
		Add(ret)
	localhostSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "localhost-serving-signer").
		Note("Rotated").
		From(installer).
		Add(ret)
	localhostCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "localhost-serving-ca").
		Note("Rotated").
		From(localhostSigner).
		Add(ret)
	localhostServing := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "localhost-serving-cert-certkey").
		Note("Rotated").
		From(localhostSigner).
		Add(ret)
	serviceNetworkSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "service-network-serving-signer").
		Note("Rotated").
		From(installer).
		Add(ret)
	serviceNetworkCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "service-network-serving-ca").
		Note("Rotated").
		From(serviceNetworkSigner).
		Add(ret)
	serviceNetworkServing := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "service-network-serving-cert-certkey").
		Note("Rotated").
		From(serviceNetworkSigner).
		From(networkConfig).
		Add(ret)
	kasServingCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "kube-apiserver-server-ca").
		Note("Unioned").
		From(loadBalancerCA).
		From(localhostCA).
		From(serviceNetworkCA).
		Add(ret)
	_ = resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-apiserver-server-ca").
		Note("Synchronized").
		From(kasServingCA).
		Add(ret)

	// well_known
	wellKnown := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "oauth-metadata").
		Note("PickOne").
		From(userWellKnown).
		From(managedWellKnown).
		Add(ret)

	// observedConfig
	config := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "config").
		Note("Managed").
		From(apiserverConfig).      // to specify client-ca, default serving, sni serving
		From(authenticationConfig). // to specify well_known
		From(imageConfig).          // to specify internal and external registries and trust
		From(kcmSATokenPub).
		From(networkConfig).
		Add(ret)

	// and finally our target pod
	_ = resourcegraph.NewResource(resourcegraph.NewCoordinates("", "pods", operatorclient.TargetNamespace, "kube-apiserver")).
		From(kasAggregatorClientCAForPod).
		From(aggregatorClient).
		From(clientCA).
		From(config).
		From(etcdServingCA).
		From(etcdClient).
		From(kubeletClient).
		From(kubeletServingCA).
		From(kcmSATokenPub).
		From(userDefaultServing).
		From(internalLBServing).
		From(externalLBServing).
		From(localhostServing).
		From(serviceNetworkServing).
		From(wellKnown).
		Add(ret)

	return ret
}
