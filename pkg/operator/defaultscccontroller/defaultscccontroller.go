package defaultscccontroller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	securityv1 "github.com/openshift/api/security/v1"
	security1informers "github.com/openshift/client-go/security/informers/externalversions"
	securityv1listers "github.com/openshift/client-go/security/listers/security/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	ControllerName = "default-scc-upgradeable"
)

type Options struct {
	Factory        security1informers.SharedInformerFactory
	OperatorClient v1helpers.OperatorClient
	Recorder       events.Recorder
}

func NewDefaultSCCController(options *Options) (controller *DefaultSCCController, err error) {
	defaultSCCSet, err := NewDefaultSCCCache()
	if err != nil {
		err = fmt.Errorf("[%s] failed to render default SCC assets - %s", ControllerName, err.Error())
		return
	}

	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "DefaultSCCController")

	informer := options.Factory.Security().V1().SecurityContextConstraints().Informer()
	informer.AddEventHandler(defaultSCCControllerEventHandler(queue))

	lister := options.Factory.Security().V1().SecurityContextConstraints().Lister()

	controller = &DefaultSCCController{
		queue:    queue,
		informer: informer,
		lister:   lister,
		recorder: options.Recorder.WithComponentSuffix(ControllerName),
		cache:    defaultSCCSet,
		updater:  NewOperatorConditionUpdater(options.OperatorClient),
	}

	return
}

// DefaultSCCController is a controller that sets upgradeable=false if any default
// SecurityContextConstraints object(s) shipped with the cluster mutates.
type DefaultSCCController struct {
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
	lister   securityv1listers.SecurityContextConstraintsLister
	recorder events.Recorder
	updater  OperatorConditionUpdater
	cache    *DefaultSCCCache
}

func (c *DefaultSCCController) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("[%s] Starting DefaultSCCController", ControllerName)
	defer klog.Infof("[%s] Shutting down DefaultSCCController", ControllerName)

	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("[%s] cache for DefaultSCCController did not sync", ControllerName))
		return
	}

	go c.runWorker()
	<-stopCh
}

func (c *DefaultSCCController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *DefaultSCCController) processNextWorkItem() bool {
	key, shutdown := c.queue.Get()

	if shutdown {
		return false
	}

	defer c.queue.Done(key)

	request, ok := key.(types.NamespacedName)
	if !ok {
		// As the item in the work queue is actually invalid, we call Forget here else
		// we'd go into a loop of attempting to process a work item that is invalid.
		c.queue.Forget(key)

		utilruntime.HandleError(fmt.Errorf("[%s] expected types.NamespacedName in workqueue but got %#v", ControllerName, key))
		return true
	}

	if err := c.Sync(request); err != nil {
		// Put the item back on the work queue to handle any transient errors.
		c.queue.AddRateLimited(key)

		utilruntime.HandleError(fmt.Errorf("[%s] key=%s error syncing, requeuing - %s", ControllerName, request, err.Error()))
		return true
	}

	c.queue.Forget(key)
	return true
}

func (c *DefaultSCCController) Sync(key types.NamespacedName) error {
	// If it's not to do with the default SCC, we don't care.
	if _, exists := c.cache.Get(key.Name); !exists {
		return nil
	}

	mutated := make([]string, 0)
	for _, name := range c.cache.DefaultSCCNames() {
		original, exists := c.cache.Get(name)
		if !exists {
			return fmt.Errorf("name=%s default scc not found in default scc cache", name)
		}

		current, err := c.lister.Get(name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				klog.Infof("[%s] name=%s scc has been deleted - %s", ControllerName, name, err.Error())
				continue
			}

			return err
		}

		// original can be mutated safely according to IsDefaultSCC, so no deep copy required.
		copy1, copy2 := withNoMeta(current), withNoMeta(original)
		if !equality.Semantic.DeepEqual(copy1, copy2) {
			mutated = append(mutated, name)
		}
	}

	if len(mutated) > 0 {
		klog.Infof("[%s] default scc has been mutated %s, please visit https://bugzilla.redhat.com/show_bug.cgi?id=1821905#c22 to resolve the issue", ControllerName, mutated)
	}

	condition := NewCondition(mutated)
	return c.updater.UpdateCondition(condition)
}

func defaultSCCControllerEventHandler(queue workqueue.RateLimitingInterface) cache.ResourceEventHandler {
	addToQueueFunc := func(key string, queue workqueue.RateLimitingInterface) {
		namespace, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			return
		}

		queue.Add(types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		})
	}

	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				klog.Errorf("[%s] OnAdd: could not extract key, type=%T- %s", ControllerName, obj, err.Error())
				return
			}

			addToQueueFunc(key, queue)
		},

		UpdateFunc: func(old, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err != nil {
				klog.Errorf("[%s] OnUpdate: could not extract key, type=%T- %s", ControllerName, new, err.Error())
				return
			}

			addToQueueFunc(key, queue)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err != nil {
				klog.Errorf("[%s] OnDelete: could not extract key, type=%T - %s", ControllerName, obj, err.Error())
				return
			}

			addToQueueFunc(key, queue)
		},
	}
}

func withNoMeta(obj *securityv1.SecurityContextConstraints) *securityv1.SecurityContextConstraints {
	copy := obj.DeepCopy()
	copy.ObjectMeta = metav1.ObjectMeta{}
	copy.TypeMeta = metav1.TypeMeta{}

	return copy
}
