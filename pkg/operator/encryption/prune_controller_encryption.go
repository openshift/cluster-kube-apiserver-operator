package encryption

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const pruneWorkKey = "key"

// encryptionPruneController prevents an unbounded growth of old encryption keys.
// For a given resource, if there are more than ten keys which have been migrated,
// this controller will delete the oldest migrated keys until there are ten migrated
// keys total.  These keys are safe to delete since no data in etcd is encrypted using
// them.  Keeping a small number of old keys around is meant to help facilitate
// decryption of old backups (and general precaution).
type encryptionPruneController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	encryptedGRs map[schema.GroupResource]bool

	targetNamespace          string
	encryptionSecretSelector metav1.ListOptions

	podClient    corev1client.PodsGetter
	secretClient corev1client.SecretsGetter
}

func newEncryptionPruneController(
	targetNamespace string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	podClient corev1client.PodsGetter,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs map[schema.GroupResource]bool,
) *encryptionPruneController {
	c := &encryptionPruneController{
		operatorClient: operatorClient,

		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionPruneController"),
		eventRecorder: eventRecorder.WithComponentSuffix("encryption-prune-controller"), // TODO unused

		encryptedGRs:    encryptedGRs,
		targetNamespace: targetNamespace,

		encryptionSecretSelector: encryptionSecretSelector,
		podClient:                podClient,
		secretClient:             secretClient,
	}

	c.preRunCachesSynced = setUpGlobalMachineConfigEncryptionInformers(operatorClient, kubeInformersForNamespaces, c.eventHandler())

	return c
}

func (c *encryptionPruneController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	// TODO do we want to use this to control the number we keep around?
	// operatorSpec.SucceededRevisionLimit

	configError := c.deleteOldMigratedSecrets()

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
	if _, _, updateError := operatorv1helpers.UpdateStaticPodStatus(c.operatorClient, operatorv1helpers.UpdateStaticPodConditionFn(cond)); updateError != nil {
		return updateError
	}

	return configError
}

func (c *encryptionPruneController) deleteOldMigratedSecrets() error {
	encryptionState, err := getDesiredEncryptionStateFromClients(c.targetNamespace, c.podClient, c.secretClient, c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err
	}

	usedSecrets := []*corev1.Secret{}
	for _, grKeys := range encryptionState {
		usedSecrets = append(usedSecrets, grKeys.readSecrets...)
	}

	allkeys, err := c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(c.encryptionSecretSelector)
	if err != nil {
		return err
	}

	var deleteErrs []error
	for _, key := range allkeys.Items {
		found := false
		for _, used := range usedSecrets {
			if used.Name == key.Name {
				found = true
				break
			}
		}
		if found {
			continue
		}

		// any secret that isn't a read key isn't used.  just delete them.
		// two phase delete: finalizer, then delete

		// remove our finalizer if it is present
		secret := key.DeepCopy()
		if finalizers := sets.NewString(secret.Finalizers...); finalizers.Has(encryptionSecretFinalizer) {
			delete(finalizers, encryptionSecretFinalizer)
			secret.Finalizers = finalizers.List()
			var updateErr error
			secret, updateErr = c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Update(secret)
			deleteErrs = append(deleteErrs, updateErr)
			if updateErr != nil {
				continue
			}
		}

		// remove the actual secret
		deleteErrs = append(deleteErrs, c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Delete(secret.Name, nil))
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
