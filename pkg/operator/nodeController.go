package operator

import (
	"fmt"
	"reflect"
	"time"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisterv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorconfiginformerv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions/kubeapiserver/v1alpha1"
)

type NodeController struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	nodeLister corelisterv1.NodeLister

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewNodeController(
	operatorConfigInformer operatorconfiginformerv1alpha1.KubeAPIServerOperatorConfigInformer,
	kubeInformersClusterScoped informers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
	kubeClient kubernetes.Interface,
) *NodeController {
	c := &NodeController{
		operatorConfigClient: operatorConfigClient,
		nodeLister:           kubeInformersClusterScoped.Core().V1().Nodes().Lister(),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NodeController"),
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	kubeInformersClusterScoped.Core().V1().Nodes().Informer().AddEventHandler(c.eventHandler())

	return c
}

func (c NodeController) sync() error {
	operatorConfig, err := c.operatorConfigClient.KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	operatorConfigOriginal := operatorConfig.DeepCopy()

	selector, err := labels.NewRequirement("node-role.kubernetes.io/master", selection.Equals, []string{""})
	if err != nil {
		panic(err)
	}
	nodes, err := c.nodeLister.List(labels.NewSelector().Add(*selector))
	if err != nil {
		return err
	}

	newTargetKubeletStates := []v1alpha1.KubeletState{}
	// remove entries for missing nodes
	for i, kubeletState := range operatorConfigOriginal.Status.TargetKubeletStates {
		found := false
		for _, node := range nodes {
			if kubeletState.NodeName == node.Name {
				found = true
			}
		}
		if found {
			newTargetKubeletStates = append(newTargetKubeletStates, operatorConfigOriginal.Status.TargetKubeletStates[i])
		}
	}

	// add entries for new nodes
	for _, node := range nodes {
		found := false
		for _, kubeletState := range operatorConfigOriginal.Status.TargetKubeletStates {
			if kubeletState.NodeName == node.Name {
				found = true
			}
		}
		if found {
			continue
		}

		newTargetKubeletStates = append(newTargetKubeletStates, v1alpha1.KubeletState{NodeName: node.Name})
	}
	operatorConfig.Status.TargetKubeletStates = newTargetKubeletStates

	if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
		_, updateError := c.operatorConfigClient.KubeAPIServerOperatorConfigs().UpdateStatus(operatorConfig)
		return updateError
	}

	return nil
}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *NodeController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting NodeController")
	defer glog.Infof("Shutting down NodeController")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *NodeController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *NodeController) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

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
func (c *NodeController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
