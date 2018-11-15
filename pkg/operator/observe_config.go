package operator

import (
	"fmt"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/configobserver"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubeinformers "k8s.io/client-go/informers"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	kubeapiserveroperatorinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
)

const configObservationErrorConditionReason = "ConfigObservationError"

type Listers struct {
	imageConfigLister configlistersv1.ImageLister
	endpointsLister   corelistersv1.EndpointsLister
	configmapLister   corelistersv1.ConfigMapLister

	imageConfigSynced cache.InformerSynced

	preRunCachesSynced []cache.InformerSynced
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.preRunCachesSynced
}

// observeConfigFunc observes configuration and returns the observedConfig. This function should not return an
// observedConfig that would cause the service being managed by the operator to crash. For example, if a required
// configuration key cannot be observed, consider reusing the configuration key's previous value. Errors that occur
// while attempting to generate the observedConfig should be returned in the errs slice.
type observeConfigFunc func(listers Listers, existingConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient configobserver.OperatorClient,
	operatorConfigInformers kubeapiserveroperatorinformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
	configInformer configinformers.SharedInformerFactory,
) *ConfigObserver {
	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			Listers{
				imageConfigLister: configInformer.Config().V1().Images().Lister(),
				endpointsLister:   kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Lister(),
				configmapLister:   kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
				imageConfigSynced: configInformer.Config().V1().Images().Informer().HasSynced,
				preRunCachesSynced: []cache.InformerSynced{
					operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().HasSynced,
					kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().HasSynced,
					kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().HasSynced,
				},
			},
			observeStorageURLs,
			observeRestrictedCIDRs,
			observeInternalRegistryHostname,
		),
	}

	operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())
	configInformer.Config().V1().Images().Informer().AddEventHandler(c.EventHandler())

	return c
}

// observeStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func observeStorageURLs(genericListers configobserver.Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	listers := genericListers.(Listers)
	observedConfig = map[string]interface{}{}
	storageConfigURLsPath := []string{"storageConfig", "urls"}
	if currentEtcdURLs, found, _ := unstructured.NestedStringSlice(currentConfig, storageConfigURLsPath...); found {
		unstructured.SetNestedStringSlice(observedConfig, currentEtcdURLs, storageConfigURLsPath...)
	}

	var etcdURLs []string
	etcdEndpoints, err := listers.endpointsLister.Endpoints(etcdNamespaceName).Get("etcd")
	if errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("endpoints/etcd.kube-system: not found"))
		return
	}
	if err != nil {
		errs = append(errs, err)
		return
	}
	dnsSuffix := etcdEndpoints.Annotations["alpha.installer.openshift.io/dns-suffix"]
	if len(dnsSuffix) == 0 {
		errs = append(errs, fmt.Errorf("endpoints/etcd.kube-system: alpha.installer.openshift.io/dns-suffix annotation not found"))
		return
	}
	for subsetIndex, subset := range etcdEndpoints.Subsets {
		for addressIndex, address := range subset.Addresses {
			if address.Hostname == "" {
				errs = append(errs, fmt.Errorf("endpoints/etcd.kube-system: subsets[%v]addresses[%v].hostname not found", subsetIndex, addressIndex))
				continue
			}
			etcdURLs = append(etcdURLs, "https://"+address.Hostname+"."+dnsSuffix+":2379")
		}
	}

	if len(etcdURLs) == 0 {
		errs = append(errs, fmt.Errorf("endpoints/etcd.kube-system: no etcd endpoint addresses found"))
	}
	if len(errs) > 0 {
		return
	}
	unstructured.SetNestedStringSlice(observedConfig, etcdURLs, storageConfigURLsPath...)
	return
}

// observeRestrictedCIDRs observes list of restrictedCIDRs.
func observeRestrictedCIDRs(genericListers configobserver.Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	listers := genericListers.(Listers)
	observedConfig = map[string]interface{}{}
	restrictedCIDRsPath := []string{"admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs"}

	clusterConfig, err := listers.configmapLister.ConfigMaps("kube-system").Get("cluster-config-v1")
	if errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: not found"))
		return
	}
	if err != nil {
		errs = append(errs, err)
		return
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config not found"))
		return
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		errs = append(errs, err)
		return
	}

	// extract needed values
	//  data:
	//   install-config:
	//     networking:
	//       podCIDR: 10.2.0.0/16
	//       serviceCIDR: 10.3.0.0/16
	var restrictedCIDRs []string
	podCIDR, found, _ := unstructured.NestedString(installConfig, "networking", "podCIDR")
	if found {
		restrictedCIDRs = append(restrictedCIDRs, podCIDR)
	} else {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config/networking/podCIDR not found"))
	}
	serviceCIDR, found, _ := unstructured.NestedString(installConfig, "networking", "serviceCIDR")
	if found {
		restrictedCIDRs = append(restrictedCIDRs, serviceCIDR)
	} else {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config/networking/serviceCIDR not found"))
	}

	// set observed values
	//  admissionPluginConfig:
	//    openshift.io/RestrictedEndpointsAdmission:
	//	  configuration:
	//	    restrictedCIDRs:
	//	    - 10.3.0.0/16 # ServiceCIDR
	//	    - 10.2.0.0/16 # ClusterCIDR
	if len(restrictedCIDRs) > 0 {
		unstructured.SetNestedStringSlice(observedConfig, restrictedCIDRs, restrictedCIDRsPath...)
	}

	return
}

// observeInternalRegistryHostname reads the internal registry hostname from the cluster configuration as provided by
// the registry operator.
func observeInternalRegistryHostname(genericListers configobserver.Listers, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	internalRegistryHostnamePath := []string{"imagePolicyConfig", "internalRegistryHostname"}
	if currentInternalRegistryHostname, _, _ := unstructured.NestedString(existingConfig, internalRegistryHostnamePath...); len(currentInternalRegistryHostname) > 0 {
		unstructured.SetNestedField(prevObservedConfig, currentInternalRegistryHostname, internalRegistryHostnamePath...)
	}

	if !listers.imageConfigSynced() {
		glog.Warning("images.config.openshift.io not synced")
		return prevObservedConfig, errs
	}

	observedConfig := map[string]interface{}{}
	configImage, err := listers.imageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		glog.Warningf("image.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return prevObservedConfig, errs
	}
	internalRegistryHostName := configImage.Status.InternalRegistryHostname
	if len(internalRegistryHostName) > 0 {
		unstructured.SetNestedField(observedConfig, internalRegistryHostName, internalRegistryHostnamePath...)
	}
	return observedConfig, errs
}
