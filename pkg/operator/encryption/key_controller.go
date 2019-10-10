package encryption

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const encWorkKey = "key"

// keyController creates new keys if necessary. It
// * watches
//   - secrets in openshift-config-managed
//   - pods in target namespace
//   - secrets in target namespace
// * computes a new, desired encryption config from encryption-config-<revision>
//   and the existing keys in openshift-config-managed.
// * derives from the desired encryption config whether a new key is needed due to
//   - encryption is being enabled via the API or
//   - a new to-be-encrypted resource shows up or
//   - the EncryptionType in the API does not match with the newest existing key or
//   - based on time (once a week is the proposed rotation interval) or
//   - an external reason given as a string in .encryption.reason of UnsupportedConfigOverrides.
//   It then creates it.
//
// Note: the "based on time" reason for a new key is based on the annotation
//       encryption.apiserver.operator.openshift.io/migrated-timestamp instead of
//       the key secret's creationTimestamp because the clock is supposed to
//       start when a migration has been finished, not when it begins.
type keyController struct {
	operatorClient  operatorv1helpers.StaticPodOperatorClient
	apiServerClient configv1client.APIServerInterface

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	encryptedGRs []schema.GroupResource

	targetNamespace          string
	encryptionSecretSelector metav1.ListOptions

	podClient    corev1client.PodsGetter
	secretClient corev1client.SecretsGetter
}

func newKeyController(
	targetNamespace string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	apiServerClient configv1client.APIServerInterface,
	apiServerInformer configv1informers.APIServerInformer,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	podClient corev1client.PodsGetter,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs []schema.GroupResource,
) *keyController {
	c := &keyController{
		operatorClient:  operatorClient,
		apiServerClient: apiServerClient,

		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionKeyController"),
		eventRecorder: eventRecorder.WithComponentSuffix("encryption-key-controller"), // TODO unused

		encryptedGRs:    encryptedGRs,
		targetNamespace: targetNamespace,

		encryptionSecretSelector: encryptionSecretSelector,
		podClient:                podClient,
		secretClient:             secretClient,
	}

	c.preRunCachesSynced = setUpAllEncryptionInformers(operatorClient, targetNamespace, kubeInformersForNamespaces, c.eventHandler())

	apiServerInformer.Informer().AddEventHandler(c.eventHandler())
	c.preRunCachesSynced = append(c.preRunCachesSynced, apiServerInformer.Informer().HasSynced)

	return c
}

func (c *keyController) sync() error {
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
	if _, _, updateError := operatorv1helpers.UpdateStaticPodStatus(c.operatorClient, operatorv1helpers.UpdateStaticPodConditionFn(cond)); updateError != nil {
		return updateError
	}

	return configError
}

func (c *keyController) checkAndCreateKeys() error {
	currentMode, externalReason, err := c.getCurrentModeAndExternalReason()
	if err != nil {
		return err
	}

	currentConfig, desiredEncryptionState, secretsFound, isProgressingReason, err := getEncryptionConfigAndState(c.podClient, c.secretClient, c.targetNamespace, c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err
	}
	if len(isProgressingReason) > 0 {
		c.queue.AddAfter(encWorkKey, 2*time.Minute)
		return nil
	}

	// avoid intended start of encryption
	hasBeenOnBefore := currentConfig != nil || secretsFound
	if currentMode == identity && !hasBeenOnBefore {
		return nil
	}

	var (
		newKeyRequired bool
		newKeyID       uint64
		reasons        []string
	)

	// note here that desiredEncryptionState is never empty because getDesiredEncryptionState
	// fills up the state with all resources and set identity write key if write key secrets
	// are missing.

	for _, grKeys := range desiredEncryptionState {
		keyID, internalReason, ok := needsNewKey(grKeys, currentMode, externalReason)
		if !ok {
			continue
		}

		newKeyRequired = true
		nextKeyID := keyID + 1
		if newKeyID < nextKeyID {
			newKeyID = nextKeyID
		}
		reasons = append(reasons, internalReason)
	}

	if !newKeyRequired {
		return nil
	}

	sort.Sort(sort.StringSlice(reasons))
	internalReason := strings.Join(reasons, ", ")
	keySecret := c.generateKeySecret(newKeyID, currentMode, internalReason, externalReason)
	_, createErr := c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Create(keySecret)
	if errors.IsAlreadyExists(createErr) {
		return c.validateExistingKey(keySecret, newKeyID)
	}
	return createErr
}

func (c *keyController) validateExistingKey(keySecret *corev1.Secret, keyID uint64) error {
	actualKeySecret, err := c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Get(keySecret.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	_, actualKeyID, validKey := secretToKeyAndMode(actualKeySecret, c.targetNamespace)
	if valid := actualKeyID == keyID && validKey; !valid {
		// TODO we can just get stuck in degraded here ...
		return fmt.Errorf("secret %s is in invalid state, new keys cannot be created for encryption target", keySecret.Name)
	}

	return nil // we made this key earlier
}

func (c *keyController) generateKeySecret(keyID uint64, currentMode mode, internalReason, externalReason string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			// this ends up looking like openshift-kube-apiserver-encryption-3
			Name:      fmt.Sprintf("%s-encryption-%d", c.targetNamespace, keyID),
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Labels: map[string]string{
				encryptionSecretComponent: c.targetNamespace,
			},
			Annotations: map[string]string{
				kubernetesDescriptionKey: kubernetesDescriptionScaryValue,

				encryptionSecretMode:           string(currentMode),
				encryptionSecretInternalReason: internalReason,
				encryptionSecretExternalReason: externalReason,
			},
			Finalizers: []string{encryptionSecretFinalizer},
		},
		Data: map[string][]byte{
			encryptionSecretKeyData: modeToNewKeyFunc[currentMode](),
		},
	}
}

func (c *keyController) getCurrentModeAndExternalReason() (mode, string, error) {
	apiServer, err := c.apiServerClient.Get("cluster", metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}

	operatorSpec, _, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return "", "", err
	}

	// TODO make this un-settable once set
	// ex: we could require the tech preview no upgrade flag to be set before we will honor this field
	type unsupportedEncryptionConfig struct {
		Encryption struct {
			Reason string `json:"reason"`
		} `json:"encryption"`
	}
	encryptionConfig := &unsupportedEncryptionConfig{}
	if raw := operatorSpec.UnsupportedConfigOverrides.Raw; len(raw) > 0 {
		jsonRaw, err := kyaml.ToJSON(raw)
		if err != nil {
			klog.Warning(err)
			// maybe it's just json
			jsonRaw = raw
		}
		if err := json.Unmarshal(jsonRaw, encryptionConfig); err != nil {
			return "", "", err
		}
	}

	reason := encryptionConfig.Encryption.Reason
	switch currentMode := mode(apiServer.Spec.Encryption.Type); currentMode {
	case aescbc, identity: // secretbox is disabled for now
		return currentMode, reason, nil
	case "": // unspecified means use the default (which can change over time)
		return defaultMode, reason, nil
	default:
		return "", "", fmt.Errorf("unknown encryption mode configured: %s", currentMode)
	}
}

// TODO unit tests
func needsNewKey(grKeys keysState, currentMode mode, externalReason string) (uint64, string, bool) {
	// if the length of read secrets is more than one (i.e. we have more than just the write key),
	// then we haven't successfully migrated and removed old keys so you should wait before generating more keys.
	if len(grKeys.readSecrets) > 1 {
		return 0, "", false
	}

	// we always need to have some encryption keys unless we are turned off
	if len(grKeys.readSecrets) == 0 {
		return 0, "no-secrets", currentMode != identity
	}

	latestKey, latestKeyID := grKeys.latestKey()

	// we have not migrated the latest key, do nothing until that is complete
	if len(latestKey.Annotations[encryptionSecretMigratedTimestamp]) == 0 {
		return 0, "", false
	}

	// if the most recent secret was encrypted in a mode different than the current mode, we need to generate a new key
	if latestKey.Annotations[encryptionSecretMode] != string(currentMode) {
		return latestKeyID, "new-mode", true
	}

	// if the most recent secret turned off encryption and we want to keep it that way, do nothing
	if latestKey.Annotations[encryptionSecretMode] == string(identity) && currentMode == identity {
		return 0, "", false
	}

	// if the most recent secret has a different external reason than the current reason, we need to generate a new key
	if latestKey.Annotations[encryptionSecretExternalReason] != externalReason && len(externalReason) != 0 {
		return latestKeyID, "new-external-reason", true
	}

	// we check for encryptionSecretMigratedTimestamp set by migration controller to determine when migration completed
	// this also generates back pressure for key rotation when migration takes a long time or was recently completed
	migrationTimestamp, err := time.Parse(time.RFC3339, latestKey.Annotations[encryptionSecretMigratedTimestamp])
	if err != nil {
		klog.Infof("failed to parse migration timestamp for %s, forcing new key: %v", latestKey.Name, err)
		return latestKeyID, "timestamp-error", true
	}

	return latestKeyID, "timestamp-too-old", time.Since(migrationTimestamp) > encryptionSecretMigrationInterval
}

func (c *keyController) run(stopCh <-chan struct{}) {
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

func (c *keyController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *keyController) processNextWorkItem() bool {
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

func (c *keyController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(encWorkKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(encWorkKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(encWorkKey) },
	}
}
