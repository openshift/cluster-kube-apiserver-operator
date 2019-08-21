package encryption

import (
	"crypto/rand"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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

const encWorkKey = "key"

type encryptionKeyController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	encryptedGRs map[schema.GroupResource]bool

	targetNamespace          string
	encryptionSecretSelector metav1.ListOptions

	secretClient corev1client.SecretInterface
}

func newEncryptionKeyController(
	targetNamespace string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs map[schema.GroupResource]bool,
) *encryptionKeyController {
	c := &encryptionKeyController{
		operatorClient: operatorClient,

		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionKeyController"),
		eventRecorder: eventRecorder.WithComponentSuffix("encryption-key-controller"), // TODO unused

		encryptedGRs:    encryptedGRs,
		targetNamespace: targetNamespace,

		encryptionSecretSelector: encryptionSecretSelector,
		secretClient:             secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace),
	}

	c.preRunCachesSynced = setUpGlobalMachineConfigEncryptionInformers(operatorClient, kubeInformersForNamespaces, c.eventHandler())

	return c
}

func (c *encryptionKeyController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	configError := c.checkAndCreateKeys()

	// update failing condition
	cond := operatorv1.OperatorCondition{
		Type:   "EncryptionKeyControllerDegraded",
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

func (c *encryptionKeyController) checkAndCreateKeys() error {
	encryptionState, err := getEncryptionState(c.secretClient, c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err
	}

	// make sure we look for all resources that we are managing
	for gr := range c.encryptedGRs {
		if _, ok := encryptionState[gr]; !ok {
			encryptionState[gr] = keysState{}
		}
	}

	var errs []error
	for gr, grKeys := range encryptionState {
		keyID, ok := needsNewKey(grKeys)
		if !ok {
			continue
		}

		nextKeyID := keyID + 1
		keySecret := c.generateKeySecret(gr, nextKeyID)
		_, createErr := c.secretClient.Create(keySecret)
		if errors.IsAlreadyExists(createErr) {
			// TODO maybe drop all of this Get logic
			actualKeySecret, getErr := c.secretClient.Get(keySecret.Name, metav1.GetOptions{})
			errs = append(errs, getErr)
			if getErr == nil {
				keyGR, _, actualKeyID, validKey := secretToKey(actualKeySecret, c.encryptedGRs)
				if valid := keyGR == gr && actualKeyID == nextKeyID && validKey; valid {
					continue // we made this key earlier
				}
				// TODO we can just get stuck in degraded here ...
				errs = append(errs, fmt.Errorf("%s secret %s is in invalid state, new keys cannot be created", gr, keySecret.Name))
			}
		}
		errs = append(errs, createErr)
	}
	// we do not filter using IsAlreadyExists as the remaining ones are actual errors
	return utilerrors.FilterOut(utilerrors.NewAggregate(errs), errors.IsNotFound)
}

func (c *encryptionKeyController) generateKeySecret(gr schema.GroupResource, keyID uint64) *corev1.Secret {
	group := gr.Group
	if len(group) == 0 {
		group = "core" // TODO may make more sense to do gr.String()
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			// this ends up looking like kube-apiserver-core-secrets-encryption-3
			Name:      fmt.Sprintf("%s-%s-%s-encryption-%d", c.targetNamespace, group, gr.Resource, keyID),
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Labels: map[string]string{
				encryptionSecretComponent: c.targetNamespace,

				encryptionSecretGroup:    gr.Group,
				encryptionSecretResource: gr.Resource,
			},
		},
		Data: map[string][]byte{
			encryptionSecretKeyData: newAES256Key(),
		},
	}
}

func needsNewKey(grKeys keysState) (uint64, bool) {
	if len(grKeys.secretsMigratedNo) > 0 {
		return 0, false
	}

	if len(grKeys.secrets) == 0 {
		return 0, true
	}

	// TODO clean up logic to get this
	lastMigrated := grKeys.secretsMigratedYes[len(grKeys.secretsMigratedYes)-1]
	keyID, _ := secretToKeyID(lastMigrated) // TODO maybe store this

	migrationTimestampStr := lastMigrated.Annotations[encryptionSecretMigratedTimestamp]
	migrationTimestamp, err := time.Parse(time.RFC3339, migrationTimestampStr)
	if err != nil {
		return keyID, true // eh?
	}

	return keyID, time.Now().After(migrationTimestamp.Add(encryptionSecretMigrationInterval))
}

func newAES256Key() []byte {
	b := make([]byte, 32) // AES-256 == 32 byte key
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func (c *encryptionKeyController) run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting EncryptionKeyController")
	defer klog.Infof("Shutting down EncryptionKeyController")
	if !cache.WaitForCacheSync(stopCh, c.preRunCachesSynced...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// only start one worker
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *encryptionKeyController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *encryptionKeyController) processNextWorkItem() bool {
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

func (c *encryptionKeyController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(encWorkKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(encWorkKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(encWorkKey) },
	}
}
