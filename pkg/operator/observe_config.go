package operator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/workqueue"

	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	kubeapiserveroperatorinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
)

type observeConfigFunc func(kubernetes.Interface, *rest.Config, map[string]interface{}) (map[string]interface{}, error)

type ConfigObserver struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	kubeClient kubernetes.Interface

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface

	rateLimiter flowcontrol.RateLimiter
	// observers are used to build the observed configuration. They are called in
	// order and are expected to mutate the config based on cluster state.
	observers []observeConfigFunc
}

func NewConfigObserver(
	operatorConfigInformers kubeapiserveroperatorinformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
	kubeClient kubernetes.Interface,
) *ConfigObserver {
	c := &ConfigObserver{
		operatorConfigClient: operatorConfigClient,
		kubeClient:           kubeClient,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ConfigObserver"),

		rateLimiter: flowcontrol.NewTokenBucketRateLimiter(0.05 /*3 per minute*/, 4),
		observers: []observeConfigFunc{
			observeEtcdEndpoints,
			observeClusterConfig,
		},
	}

	operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().Endpoints().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())

	return c
}

// sync reacts to a change in prereqs by finding information that is required to match another value in the cluster. This
// must be information that is logically "owned" by another component.
func (c ConfigObserver) sync() error {

	observedConfig := map[string]interface{}{}
	var err error

	for _, observer := range c.observers {
		observedConfig, err = observer(c.kubeClient, &rest.Config{}, observedConfig)
		if err != nil {
			return err
		}
	}

	operatorConfig, err := c.operatorConfigClient.KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	// don't worry about errors
	currentConfig := map[string]interface{}{}
	json.NewDecoder(bytes.NewBuffer(operatorConfig.Spec.ObservedConfig.Raw)).Decode(&currentConfig)
	if reflect.DeepEqual(currentConfig, observedConfig) {
		return nil
	}

	glog.Infof("writing updated observedConfig: %v", diff.ObjectDiff(operatorConfig.Spec.ObservedConfig.Object, observedConfig))
	operatorConfig.Spec.ObservedConfig = runtime.RawExtension{Object: &unstructured.Unstructured{Object: observedConfig}}
	if _, err := c.operatorConfigClient.KubeAPIServerOperatorConfigs().Update(operatorConfig); err != nil {
		return err
	}

	return nil
}

// observeEtcdEndpoints reads the etcd endpoints from the endpoints object and then manually pull out the hostnames to
// get the etcd urls for our config. Setting them observed config causes the normal reconciliation loop to run
func observeEtcdEndpoints(kubeClient kubernetes.Interface, clientConfig *rest.Config, observedConfig map[string]interface{}) (map[string]interface{}, error) {
	etcdURLs := []string{}
	etcdEndpoints, err := kubeClient.CoreV1().Endpoints(etcdNamespaceName).Get("etcd", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return observedConfig, nil
	}
	if err != nil {
		return observedConfig, err
	}
	for _, subset := range etcdEndpoints.Subsets {
		for _, address := range subset.Addresses {
			etcdURLs = append(etcdURLs, "https://"+address.Hostname+"."+etcdEndpoints.Annotations["alpha.installer.openshift.io/dns-suffix"]+":2379")
		}
	}
	if len(etcdURLs) > 0 {
		unstructured.SetNestedStringSlice(observedConfig, etcdURLs, "storageConfig", "urls")
	} else {
		glog.Warningf("no etcd endpoints found")
	}
	return observedConfig, nil
}

// observeClusterConfig observes CIDRs from cluster-config-v1 in order to populate list of restrictedCIDRs
func observeClusterConfig(kubeClient kubernetes.Interface, clientConfig *rest.Config, observedConfig map[string]interface{}) (map[string]interface{}, error) {
	clusterConfig, err := kubeClient.CoreV1().ConfigMaps("kube-system").Get("cluster-config-v1", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		glog.Warningf("cluster-config-v1 not found in the kube-system namespace")
		return observedConfig, nil
	}
	if err != nil {
		return observedConfig, err
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		return observedConfig, nil
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		glog.Warningf("Unable to parse install-config: %s", err)
		return observedConfig, nil
	}

	// extract needed values
	//  data:
	//   install-config:
	//     networking:
	//       podCIDR: 10.2.0.0/16
	//       serviceCIDR: 10.3.0.0/16
	restrictedCIDRs := []string{}
	networking, ok := installConfig["networking"].(map[string]interface{})
	if !ok {
		return observedConfig, nil
	}
	if cidr := networking["podCIDR"]; cidr != nil {
		restrictedCIDRs = append(restrictedCIDRs, fmt.Sprintf("%v", cidr))
	} else {
		glog.Warningf("No value found for install-config/networking/podCIDR.")
	}
	if cidr := networking["serviceCIDR"]; cidr != nil {
		restrictedCIDRs = append(restrictedCIDRs, fmt.Sprintf("%v", cidr))
	} else {
		glog.Warningf("No value found for install-config/networking/serviceCIDR.")
	}
	// set observed values
	//  admissionPluginConfig:
	//    openshift.io/RestrictedEndpointsAdmission:
	//	  configuration:
	//	    restrictedCIDRs:
	//	    - 10.3.0.0/16 # ServiceCIDR
	//	    - 10.2.0.0/16 # ClusterCIDR
	if len(restrictedCIDRs) > 0 {
		unstructured.SetNestedStringSlice(observedConfig, restrictedCIDRs,
			"admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs")
	}

	return observedConfig, nil
}

func (c *ConfigObserver) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting ConfigObserver")
	defer glog.Infof("Shutting down ConfigObserver")

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
