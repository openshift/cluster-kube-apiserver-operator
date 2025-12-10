package certregenerationcontroller

import (
	"context"
	"fmt"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
)

const workQueueKey = "key"

// CABundleController composes individual certs into CA bundle that is used
// by kube-apiserver to validate clients.
// Cert recovery refreshes "kube-control-plane-signer-ca" and needs the containing
// bundle regenerated so kube-controller-manager and kube-scheduler can connect
// using client certs.
type CABundleController struct {
	configMapGetter corev1client.ConfigMapsGetter
	configMapLister corev1listers.ConfigMapLister

	eventRecorder events.Recorder

	cachesToSync []cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewCABundleController(
	configMapGetter corev1client.ConfigMapsGetter,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
) (*CABundleController, error) {
	c := &CABundleController{
		configMapGetter: configMapGetter,
		configMapLister: kubeInformersForNamespaces.ConfigMapLister(),
		eventRecorder:   eventRecorder.WithComponentSuffix("manage-client-ca-bundle-recovery-controller"),
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "CABundleRecoveryController"),
	}

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}

	// we react to some config changes
	namespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	}
	for _, namespace := range namespaces {
		informers := kubeInformersForNamespaces.InformersFor(namespace)
		informers.Core().V1().ConfigMaps().Informer().AddEventHandler(handler)
		c.cachesToSync = append(c.cachesToSync, informers.Core().V1().ConfigMaps().Informer().HasSynced)
	}

	return c, nil
}

func (c *CABundleController) Run(ctx context.Context) {
	defer utilruntime.HandleCrashWithContext(ctx)

	klog.Info("Starting CA bundle controller")
	var wg sync.WaitGroup
	defer func() {
		klog.Info("Shutting down CA bundle controller")
		c.queue.ShutDown()
		wg.Wait()
		klog.Info("CA bundle controller shut down")
	}()

	if !cache.WaitForNamedCacheSync("CABundleController", ctx.Done(), c.cachesToSync...) {
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}()

	<-ctx.Done()
}

func (c *CABundleController) runWorker(ctx context.Context) {
	for c.processNextItem(ctx) {
	}
}

func (c *CABundleController) processNextItem(ctx context.Context) bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	err := c.sync(ctx)

	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %w", key, err))
	c.queue.AddRateLimited(key)

	return true
}

func (c *CABundleController) sync(ctx context.Context) error {
	// Always start 10 seconds later after a change occurred. Makes us less likely to steal work and logs from the operator.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil
	}

	_, changed, err := targetconfigcontroller.ManageClientCABundle(ctx, c.configMapLister, c.configMapGetter, c.eventRecorder)
	if err != nil {
		return err
	}

	if changed {
		klog.V(2).Info("Refreshed client CA bundle.")
	}

	return nil
}
