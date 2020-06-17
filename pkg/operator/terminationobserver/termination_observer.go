package terminationobserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog"

	"github.com/openshift/library-go/pkg/operator/events"
)

var (
	controllerWorkQueueKey = "key"

	// terminationEventReasons lists all events that are observed when an API server is shutting down gracefully.
	terminationEventReasons = []string{
		"TerminationStart",
		"TerminationPreShutdownHooksFinished",
		"TerminationPreShutdownHooksFinished",
		"TerminationMinimalShutdownDurationFinished",
		"TerminationStoppedServing",
		"TerminationGracefulTerminationFinished",
	}
)

// TerminationObserver observes static pods that are terminating. When the API server static pod is replaced by
// new revision or the pod is evicted or removed, the static pods are not reporting the terminating state back
// to API server, but they only change the creationTimestamp.
// We need to capture the termination events produced by the pods that we no longer see.
type TerminationObserver struct {
	targetNamespace string

	podsGetter corev1client.PodsGetter

	cachesToSync  []cache.InformerSynced
	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	apiServerTerminationTime map[string]time.Time
	sync.RWMutex
}

var (
	registerMetrics sync.Once

	apiServerTerminationEventGauge = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name: "openshift_kube_apiserver_termination_event_time",
		Help: "Report times of termination events observed for individual API server instances",
	}, []string{"name", "eventName"})

	apiServerTerminationCounter = metrics.NewCounterVec(&metrics.CounterOpts{
		Name: "openshift_kube_apiserver_termination_count",
		Help: "Report termination count for each API server instance over time",
	}, []string{"name"})
)

func RegisterMetrics() {
	registerMetrics.Do(func() {
		legacyregistry.MustRegister(apiServerTerminationEventGauge)
		legacyregistry.MustRegister(apiServerTerminationCounter)
		legacyregistry.MustRegister(apiServerLateConnectionsCounter)
	})
}

func NewTerminationObserver(
	targetNamespace string,
	kubeInformersForTargetNamespace informers.SharedInformerFactory,
	podsGetter corev1client.PodsGetter,
	eventRecorder events.Recorder,
) *TerminationObserver {
	c := &TerminationObserver{
		targetNamespace:          targetNamespace,
		podsGetter:               podsGetter,
		eventRecorder:            eventRecorder.WithComponentSuffix("termination-observer"),
		queue:                    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "TerminationObserver"),
		apiServerTerminationTime: map[string]time.Time{},
	}

	kubeInformersForTargetNamespace.Core().V1().Pods().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForTargetNamespace.Core().V1().Events().Informer().AddEventHandler(c.terminationEventRecorder())

	c.cachesToSync = append(c.cachesToSync, kubeInformersForTargetNamespace.Core().V1().Pods().Informer().HasSynced)
	c.cachesToSync = append(c.cachesToSync, kubeInformersForTargetNamespace.Core().V1().Events().Informer().HasSynced)

	return c
}

func (c *TerminationObserver) sync(ctx context.Context) error {
	podList, err := c.podsGetter.Pods(c.targetNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=openshift-kube-apiserver"})
	if err != nil {
		return fmt.Errorf("unable to list pods in %q namespace: %v", c.targetNamespace, err)
	}

	c.Lock()
	defer c.Unlock()

	for _, pod := range podList.Items {
		// Prevent firing termination logs and metrics for initial observation (we don't know when the API Server was terminated).
		if _, exists := c.apiServerTerminationTime[pod.Name]; !exists {
			c.apiServerTerminationTime[pod.Name] = pod.CreationTimestamp.Time
			continue
		}

		// Record the creationTimestamp as "termination" time when the creation timestamp changed.
		// When creationTimestamp change for a static pod, it signals the pod was replaced.
		// Kubelet does not report "terminating", it will just bump the creationTimestamp, which is the only way for us to see that the containers
		// in the pod were recreated.
		if pod.CreationTimestamp.Time != c.apiServerTerminationTime[pod.Name] {

			// StaticPodRecreated is "fake" event that tracks observation of "static pod content was replaced".
			apiServerTerminationEventGauge.WithLabelValues(pod.Name, "StaticPodRecreated").Set(float64(pod.CreationTimestamp.Time.Unix()))

			// increase the "termination" counter for this API server.
			apiServerTerminationCounter.WithLabelValues(pod.Name).Inc()

			// record the current pod creationTimestamp as "termination" timestamp for the previous API server.
			c.apiServerTerminationTime[pod.Name] = pod.CreationTimestamp.Time
			klog.Infof("Observed termination of API server pod %q at %s", pod.Name, pod.CreationTimestamp.Time)
		}
	}

	return nil
}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *TerminationObserver) Run(ctx context.Context, workers int) {
	if workers > 1 {
		panic("only one worker is supported in termination observer ")
	}
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting TerminationObserver")
	defer klog.Infof("Shutting down TerminationObserver")
	if !cache.WaitForCacheSync(ctx.Done(), c.cachesToSync...) {
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.UntilWithContext(ctx, c.runWorker, time.Second)

	<-ctx.Done()
}

func (c *TerminationObserver) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *TerminationObserver) processNextWorkItem(ctx context.Context) bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.sync(ctx)
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)
	return true
}

// isApiServerEvent return true if the event involved object contain known API server pod name
func isApiServerEvent(event *corev1.Event, apiServers []string) bool {
	for _, name := range apiServers {
		if event.InvolvedObject.Kind == "Pod" && event.InvolvedObject.Name == name {
			return true
		}
	}
	return false
}

// isTerminationEvent return true if the event is known API server termination event
func isTerminationEvent(event *corev1.Event) bool {
	for _, reason := range terminationEventReasons {
		if event.Reason == reason {
			return true
		}
	}
	return false
}

func (c *TerminationObserver) apiServerNames() []string {
	c.RLock()
	defer c.RUnlock()
	var names []string
	for name := range c.apiServerTerminationTime {
		names = append(names, name)
	}
	return names
}

// terminationEventRecorder records API server termination events for each API server.
func (c *TerminationObserver) terminationEventRecorder() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event, ok := obj.(*corev1.Event)
			if !ok {
				utilruntime.HandleError(fmt.Errorf("expected v1.Event, got %T", obj))
				return
			}
			if !isApiServerEvent(event, c.apiServerNames()) {
				return
			}
			if !isTerminationEvent(event) {
				return
			}

			c.RLock()
			apiServerTerminationTime, ok := c.apiServerTerminationTime[event.InvolvedObject.Name]
			c.RUnlock()
			if !ok {
				klog.Warningf("Observed %q event for unknown API server pod: %q", event.Reason, event.InvolvedObject.Name)
				return
			}

			apiServerTerminationEventGauge.WithLabelValues(event.InvolvedObject.Name, event.Reason).Set(float64(event.LastTimestamp.Unix()))

			klog.Infof("Observed event %q for API server pod %q (last termination at %s) at %s", event.Reason, event.InvolvedObject.Name, apiServerTerminationTime, event.LastTimestamp.Time)
		},

		// events can't be updated or deleted
		UpdateFunc: func(old, new interface{}) {},
		DeleteFunc: func(obj interface{}) {},
	}
}

func (c *TerminationObserver) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(controllerWorkQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(controllerWorkQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(controllerWorkQueueKey) },
	}
}
