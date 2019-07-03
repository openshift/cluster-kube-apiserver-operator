package encryption

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const pruneWorkKey = "key"

type encryptionPruneController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder // TODO this is currently not used

	preRunCachesSynced []cache.InformerSynced

	validGRs map[schema.GroupResource]bool

	componentSelector labels.Selector

	secretLister corev1listers.SecretNamespaceLister
	secretClient corev1client.SecretInterface
}

func newEncryptionPruneController(
	targetNamespace string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	validGRs map[schema.GroupResource]bool,
) *encryptionPruneController {
	c := &encryptionPruneController{
		operatorClient: operatorClient,
		eventRecorder:  eventRecorder.WithComponentSuffix("encryption-prune-controller"),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionPruneController"),

		preRunCachesSynced: []cache.InformerSynced{
			operatorClient.Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer().HasSynced,
		},

		validGRs: validGRs,
	}

	c.componentSelector = labelSelectorOrDie(encryptionSecretComponent + "=" + targetNamespace)

	operatorClient.Informer().AddEventHandler(c.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer().AddEventHandler(c.eventHandler())

	c.secretLister = kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).
		Core().V1().Secrets().Lister().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)
	c.secretClient = kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)

	return c
}

func (c *encryptionPruneController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	// TODO do we want to use this to control the number we keep around?
	// operatorSpec.SucceededRevisionLimit

	configError := c.handleEncryptionPrune()

	// update failing condition
	cond := operatorv1.OperatorCondition{
		Type:   "EncryptionPruneControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if configError != nil {
		cond.Status = operatorv1.ConditionTrue
		cond.Reason = "Error"
		cond.Message = configError.Error()
	}
	if _, _, updateError := operatorv1helpers.UpdateStatus(c.operatorClient, operatorv1helpers.UpdateConditionFn(cond)); updateError != nil {
		return updateError
	}

	return configError
}

func (c *encryptionPruneController) handleEncryptionPrune() error {
	encryptionSecrets, err := c.secretLister.List(c.componentSelector)
	if err != nil {
		return err
	}

	encryptionState := getEncryptionState(encryptionSecrets, c.validGRs)

	var deleteErrs []error
	for _, grKeys := range encryptionState {
		// TODO see SucceededRevisionLimit comment above, cannot exceed 100, see secretToKey func
		deleteCount := len(grKeys.secretsMigratedYes) - 10
		if deleteCount <= 0 {
			continue
		}
		for _, secret := range grKeys.secretsMigratedYes[:deleteCount] {
			deleteErrs = append(deleteErrs, c.secretClient.Delete(secret.Name, nil))
		}
	}
	return utilerrors.FilterOut(utilerrors.NewAggregate(deleteErrs), errors.IsNotFound)
}

func (c *encryptionPruneController) run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting EncryptionPruneController")
	defer klog.Infof("Shutting down EncryptionPruneController")
	if !cache.WaitForCacheSync(stopCh, c.preRunCachesSynced...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// only start one worker
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *encryptionPruneController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *encryptionPruneController) processNextWorkItem() bool {
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

	utilruntime.HandleError(fmt.Errorf("%v failed with: %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

func (c *encryptionPruneController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(pruneWorkKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(pruneWorkKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(pruneWorkKey) },
	}
}
