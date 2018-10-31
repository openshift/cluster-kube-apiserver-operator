package operator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/imdario/mergo"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/api/operator/v1alpha1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	kubeapiserveroperatorinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

const configObservationErrorConditionReason = "ConfigObservationError"

type Listers struct {
	imageConfigLister configlistersv1.ImageLister
	endpointsLister   corelistersv1.EndpointsLister
	configmapLister   corelistersv1.ConfigMapLister
}

// observeConfigFunc observes configuration and returns the observedConfig. This function should not return an
// observedConfig that would cause the service being managed by the operator to crash. For example, if a required
// configuration key cannot be observed, consider reusing the configuration key's previous value. Errors that occur
// while attempting to generate the observedConfig should be returned in the errs slice.
type observeConfigFunc func(listers Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error)

type ConfigObserver struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface

	rateLimiter flowcontrol.RateLimiter
	// observers are called in an undefined order and their results are merged to
	// determine the observed configuration.
	observers []observeConfigFunc

	// listers are used by config observers to retrieve necessary resources
	listers Listers

	operatorConfigSynced cache.InformerSynced
	endpointsSynced      cache.InformerSynced
	configmapSynced      cache.InformerSynced
	configSynced         cache.InformerSynced
}

func NewConfigObserver(
	operatorConfigInformers kubeapiserveroperatorinformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
	configInformer configinformers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
) *ConfigObserver {
	c := &ConfigObserver{
		operatorConfigClient: operatorConfigClient,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ConfigObserver"),

		rateLimiter: flowcontrol.NewTokenBucketRateLimiter(0.05 /*3 per minute*/, 4),
		observers: []observeConfigFunc{
			observeStorageURLs,
			observeRestrictedCIDRs,
			observeInternalRegistryHostname,
		},
		listers: Listers{
			imageConfigLister: configInformer.Config().V1().Images().Lister(),
			endpointsLister:   kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Lister(),
			configmapLister:   kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
		},
	}

	c.operatorConfigSynced = operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().HasSynced
	c.endpointsSynced = kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().HasSynced
	c.configmapSynced = kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().HasSynced
	c.configSynced = configInformer.Config().V1().Images().Informer().HasSynced

	operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	configInformer.Config().V1().Images().Informer().AddEventHandler(c.eventHandler())

	return c
}

// sync reacts to a change in prereqs by finding information that is required to match another value in the cluster. This
// must be information that is logically "owned" by another component.
func (c ConfigObserver) sync() error {

	operatorConfig, err := c.operatorConfigClient.KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	// don't worry about errors
	currentConfig := map[string]interface{}{}
	json.NewDecoder(bytes.NewBuffer(operatorConfig.Spec.ObservedConfig.Raw)).Decode(&currentConfig)

	var errs []error
	var observedConfigs []map[string]interface{}
	for _, i := range rand.Perm(len(c.observers)) {
		var currErrs []error
		observedConfig, currErrs := c.observers[i](c.listers, currentConfig)
		observedConfigs = append(observedConfigs, observedConfig)
		errs = append(errs, currErrs...)
	}

	mergedObservedConfig := map[string]interface{}{}
	for _, observedConfig := range observedConfigs {
		mergo.Merge(&mergedObservedConfig, observedConfig)
	}

	if !equality.Semantic.DeepEqual(currentConfig, mergedObservedConfig) {
		glog.Infof("writing updated observedConfig: %v", diff.ObjectDiff(operatorConfig.Spec.ObservedConfig.Object, mergedObservedConfig))
		operatorConfig.Spec.ObservedConfig = runtime.RawExtension{Object: &unstructured.Unstructured{Object: mergedObservedConfig}}
		operatorConfig, err = c.operatorConfigClient.KubeAPIServerOperatorConfigs().Update(operatorConfig)
		if err != nil {
			errs = append(errs, fmt.Errorf("kubeapiserveroperatorconfigs/instance: error writing updated observed config: %v", err))
		}
	}

	status := operatorConfig.Status.DeepCopy()
	if len(errs) > 0 {
		var messages []string
		for _, currentError := range errs {
			messages = append(messages, currentError.Error())
		}
		v1alpha1helpers.SetOperatorCondition(&status.Conditions, v1alpha1.OperatorCondition{
			Type:    v1alpha1.OperatorStatusTypeFailing,
			Status:  v1alpha1.ConditionTrue,
			Reason:  configObservationErrorConditionReason,
			Message: strings.Join(messages, "\n"),
		})
	} else {
		condition := v1alpha1helpers.FindOperatorCondition(status.Conditions, v1alpha1.OperatorStatusTypeFailing)
		if condition != nil && condition.Status != v1alpha1.ConditionFalse && condition.Reason == configObservationErrorConditionReason {
			condition.Status = v1alpha1.ConditionFalse
			condition.Reason = ""
			condition.Message = ""
		}
	}

	if !equality.Semantic.DeepEqual(operatorConfig.Status, status) {
		operatorConfig.Status = *status
		_, err = c.operatorConfigClient.KubeAPIServerOperatorConfigs().UpdateStatus(operatorConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

// observeStorageURLs observes the storage config URLs. If there is a problem observing the current storage config URLs,
// then the previously observed storage config URLs will be re-used.
func observeStorageURLs(listers Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
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
func observeRestrictedCIDRs(listers Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
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
func observeInternalRegistryHostname(listers Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	observedConfig = map[string]interface{}{}
	internalRegistryHostnamePath := []string{"imagePolicyConfig", "internalRegistryHostname"}
	currentInternalRegistryHostname, _, _ := unstructured.NestedStringSlice(currentConfig, internalRegistryHostnamePath...)

	configImage, err := listers.imageConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		glog.Warningf("image.config.openshift.io/cluster: not found")
		return
	}
	if err != nil {
		errs = append(errs, err)
		if len(currentInternalRegistryHostname) > 0 {
			unstructured.SetNestedField(observedConfig, currentInternalRegistryHostname, internalRegistryHostnamePath...)
		}
		return
	}
	internalRegistryHostName := configImage.Status.InternalRegistryHostname
	if len(internalRegistryHostName) > 0 {
		unstructured.SetNestedField(observedConfig, internalRegistryHostName, internalRegistryHostnamePath...)
	}
	return
}

func (c *ConfigObserver) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting ConfigObserver")
	defer glog.Infof("Shutting down ConfigObserver")

	cache.WaitForCacheSync(stopCh,
		c.operatorConfigSynced,
		c.endpointsSynced,
		c.configmapSynced,
		c.configSynced,
	)

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *ConfigObserver) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *ConfigObserver) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	// before we call sync, we want to wait for token.  We do this to avoid hot looping.
	c.rateLimiter.Accept()

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *ConfigObserver) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
