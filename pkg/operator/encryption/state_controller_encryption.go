package encryption

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
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

// encryptionStateController is responsible for creating a single secret in
// openshift-config-managed with the name destName.  This single secret
// contains the complete EncryptionConfiguration that is consumed by the API
// server that is performing the encryption.  Thus this secret represents
// the current state of all resources in encryptedGRs.  Every encryption key
// that matches encryptionSecretSelector is included in this final secret.
// This secret is synced into targetNamespace at a static location.  This
// indirection allows the cluster to recover from the deletion of targetNamespace.
// See getResourceConfigs for details on how the raw state of all keys
// is converted into a single encryption config.  The logic for determining
// the current write key is of special interest.
type encryptionStateController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	encryptedGRs map[schema.GroupResource]bool

	destName                 string
	targetNamespace          string
	encryptionSecretSelector metav1.ListOptions

	secretClient corev1client.SecretsGetter

	podClient corev1client.PodInterface
}

func newEncryptionStateController(
	targetNamespace, destName string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs map[schema.GroupResource]bool,
	podClient corev1client.PodInterface,
) *encryptionStateController {
	c := &encryptionStateController{
		operatorClient: operatorClient,

		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionStateController"),
		eventRecorder: eventRecorder.WithComponentSuffix("encryption-state-controller"),

		encryptedGRs:    encryptedGRs,
		destName:        destName,
		targetNamespace: targetNamespace,

		encryptionSecretSelector: encryptionSecretSelector,
		secretClient:             secretClient,
		podClient:                podClient,
	}

	c.preRunCachesSynced = setUpAllEncryptionInformers(operatorClient, targetNamespace, kubeInformersForNamespaces, c.eventHandler())

	return c
}

func (c *encryptionStateController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	configError := c.generateAndApplyCurrentEncryptionConfigSecret()

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
	if _, _, updateError := operatorv1helpers.UpdateStaticPodStatus(c.operatorClient, operatorv1helpers.UpdateStaticPodConditionFn(cond)); updateError != nil {
		return updateError
	}

	return configError
}

func (c *encryptionStateController) generateAndApplyCurrentEncryptionConfigSecret() error {
	// do not cause new revisions while old ones are rolling out
	// TODO but does this even matter?
	revision, err := getAPIServerRevisionOfAllInstances(c.podClient)
	if len(revision) == 0 && err == nil {
		c.queue.AddAfter(stateWorkKey, 2*time.Minute)
		return nil
	}
	if err != nil {
		return err
	}

	encryptionState, err := getEncryptionState(c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace), c.targetNamespace, c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err
	}

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
			Annotations: map[string]string{
				kubernetesDescriptionKey: kubernetesDescriptionScaryValue,
			},
			Finalizers: []string{encryptionSecretFinalizer},
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
