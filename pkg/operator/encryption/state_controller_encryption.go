package encryption

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const stateWorkKey = "key"

type encryptionStateController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	validGRs map[schema.GroupResource]bool

	targetNamespace   string
	destName          string
	componentSelector labels.Selector

	// TODO fix and combine
	secretLister corev1listers.SecretLister
	secretClient corev1client.SecretsGetter

	podClient corev1client.PodInterface
}

func newEncryptionStateController(
	targetNamespace, destName string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	secretClient corev1client.SecretsGetter,
	podClient corev1client.PodsGetter,
	eventRecorder events.Recorder,
	validGRs map[schema.GroupResource]bool,
) *encryptionStateController {
	c := &encryptionStateController{
		operatorClient: operatorClient,
		eventRecorder:  eventRecorder.WithComponentSuffix("encryption-state-controller"),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionStateController"),

		preRunCachesSynced: []cache.InformerSynced{
			operatorClient.Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Pods().Informer().HasSynced,
		},

		validGRs: validGRs,

		targetNamespace: targetNamespace,
		destName:        destName,
	}

	c.componentSelector = labelSelectorOrDie(encryptionSecretComponent + "=" + targetNamespace)

	operatorClient.Informer().AddEventHandler(c.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Pods().Informer().AddEventHandler(c.eventHandler())

	c.secretLister = kubeInformersForNamespaces.InformersFor("").Core().V1().Secrets().Lister()
	c.secretClient = secretClient
	c.podClient = podClient.Pods(targetNamespace)

	return c
}

func (c *encryptionStateController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	configError := c.handleEncryptionStateConfig()

	// update failing condition
	cond := operatorv1.OperatorCondition{
		Type:   "EncryptionStateControllerDegraded",
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

func (c *encryptionStateController) handleEncryptionStateConfig() error {
	// do not cause new revisions while old ones are rolling out
	// TODO but does this even matter?
	revision, err := getAPIServerRevision(c.podClient)
	if err != nil || len(revision) == 0 {
		return err
	}

	encryptionSecrets, err := c.secretLister.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(c.componentSelector)
	if err != nil {
		return err
	}

	encryptionState := getEncryptionState(encryptionSecrets, c.validGRs)

	resourceConfigs := getResourceConfigs(encryptionState)

	// if we have no config, do not create the secret
	if len(resourceConfigs) == 0 {
		return nil
	}

	return c.applyEncryptionConfigSecret(resourceConfigs)
}

func (c *encryptionStateController) applyEncryptionConfigSecret(resourceConfigs []apiserverconfigv1.ResourceConfiguration) error {
	encryptionConfig := &apiserverconfigv1.EncryptionConfiguration{Resources: resourceConfigs}
	encryptionConfigBytes, err := runtime.Encode(encoder, encryptionConfig)
	if err != nil {
		return err // indicates static generated code is broken, unrecoverable
	}

	_, _, applyErr := resourceapply.ApplySecret(c.secretClient, c.eventRecorder, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.destName,
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		},
		Data: map[string][]byte{encryptionConfSecret: encryptionConfigBytes},
	})
	return applyErr
}

func (c *encryptionStateController) run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting EncryptionStateController")
	defer klog.Infof("Shutting down EncryptionStateController")
	if !cache.WaitForCacheSync(stopCh, c.preRunCachesSynced...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// only start one worker
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *encryptionStateController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *encryptionStateController) processNextWorkItem() bool {
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

func (c *encryptionStateController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(stateWorkKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(stateWorkKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(stateWorkKey) },
	}
}
