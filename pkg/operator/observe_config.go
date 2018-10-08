package operator

import (
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/workqueue"

	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorconfiginformerv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions/kubeapiserver/v1alpha1"
)

type ConfigObserver struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	kubeClient kubernetes.Interface

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface

	rateLimiter flowcontrol.RateLimiter
}

func NewConfigObserver(
	operatorConfigInformer operatorconfiginformerv1alpha1.KubeApiserverOperatorConfigInformer,
	etcdNamespacedKubeInformers kubeinformers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
	kubeClient kubernetes.Interface,
) *ConfigObserver {
	c := &ConfigObserver{
		operatorConfigClient: operatorConfigClient,
		kubeClient:           kubeClient,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ConfigObserver"),

		rateLimiter: flowcontrol.NewTokenBucketRateLimiter(0.05 /*3 per minute*/, 4),
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	etcdNamespacedKubeInformers.Core().V1().Endpoints().Informer().AddEventHandler(c.eventHandler())

	return c
}

// sync reacts to a change in prereqs by finding information that is required to match another value in the cluster. This
// must be information that is logically "owned" by another component.
func (c ConfigObserver) sync() error {
	operatorConfig, err := c.operatorConfigClient.KubeApiserverOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}
	observedConfig := map[string]interface{}{}
	etcdURLs := []string{}

	// we read the etcd endpoints from the endpoints object and then manually pull out the hostnames to get the etcd urls for our config.
	// Setting them observed config causes the normal reconciliation loop to run
	etcdEndpoints, err := c.kubeClient.CoreV1().Endpoints(etcdNamespaceName).Get("etcd", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if etcdEndpoints != nil {
		for _, subset := range etcdEndpoints.Subsets {
			for _, address := range subset.Addresses {
				etcdURLs = append(etcdURLs, "https://"+address.Hostname+":2379")
			}
		}
	}
	unstructured.SetNestedField(observedConfig, etcdURLs, "storageConfig", "urls")

	// TODO fix the comparison here, I think we're constantly updating
	if reflect.DeepEqual(operatorConfig.Spec.ObservedConfig.Object, observedConfig) {
		return nil
	}
	operatorConfig.Spec.ObservedConfig = runtime.RawExtension{Object: &unstructured.Unstructured{Object: observedConfig}}
	if _, err := c.operatorConfigClient.KubeApiserverOperatorConfigs().Update(operatorConfig); err != nil {
		return err
	}

	return nil
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
