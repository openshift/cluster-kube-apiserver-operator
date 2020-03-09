package controllers

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/encryption/secrets"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	"github.com/openshift/library-go/pkg/operator/encryption/statemachine"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	keepNumberOfSecrets = 10
)

// pruneController prevents an unbounded growth of old encryption keys.
// For a given resource, if there are more than ten keys which have been migrated,
// this controller will delete the oldest migrated keys until there are ten migrated
// keys total.  These keys are safe to delete since no data in etcd is encrypted using
// them.  Keeping a small number of old keys around is meant to help facilitate
// decryption of old backups (and general precaution).
type pruneController struct {
	operatorClient operatorv1helpers.OperatorClient

	encryptedGRs []schema.GroupResource

	encryptionSecretSelector metav1.ListOptions

	deployer     statemachine.Deployer
	secretClient corev1client.SecretsGetter
	name         string
}

func NewPruneController(
	deployer statemachine.Deployer,
	operatorClient operatorv1helpers.OperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs []schema.GroupResource,
) factory.Controller {
	c := &pruneController{
		operatorClient:           operatorClient,
		name:                     "EncryptionPruneController",
		encryptedGRs:             encryptedGRs,
		encryptionSecretSelector: encryptionSecretSelector,
		deployer:                 deployer,
		secretClient:             secretClient,
	}

	return factory.New().ResyncEvery(time.Second).WithSync(c.sync).WithInformers(
		operatorClient.Informer(),
		kubeInformersForNamespaces.InformersFor("openshift-config-managed").Core().V1().Secrets().Informer(),
		deployer,
	).ToController(c.name, eventRecorder.WithComponentSuffix("encryption-prune-controller"))
}

func (c *pruneController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	configError := c.deleteOldMigratedSecrets(syncCtx.Queue(), syncCtx.Recorder())

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

func (c *pruneController) deleteOldMigratedSecrets(queue workqueue.RateLimitingInterface, recorder events.Recorder) error {
	_, desiredEncryptionConfig, _, isProgressingReason, err := statemachine.GetEncryptionConfigAndState(c.deployer, c.secretClient, c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err
	}
	if len(isProgressingReason) > 0 {
		queue.AddAfter(factory.QueueKey(c.name), 2*time.Minute)
		return nil
	}

	allUsedKeys := make([]state.KeyState, 0, len(desiredEncryptionConfig))
	for _, grKeys := range desiredEncryptionConfig {
		allUsedKeys = append(allUsedKeys, grKeys.ReadKeys...)
	}

	allSecrets, err := c.secretClient.Secrets("openshift-config-managed").List(c.encryptionSecretSelector)
	if err != nil {
		return err
	}

	// sort by keyID
	encryptionSecrets := make([]*corev1.Secret, 0, len(allSecrets.Items))
	for _, s := range allSecrets.Items {
		encryptionSecrets = append(encryptionSecrets, s.DeepCopy()) // don't use &s because it is constant through-out the loop
	}
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		iKeyID, _ := state.NameToKeyID(encryptionSecrets[i].Name)
		jKeyID, _ := state.NameToKeyID(encryptionSecrets[j].Name)
		return iKeyID > jKeyID
	})

	var deleteErrs []error
	skippedKeys := 0
	deletedKeys := 0
NextEncryptionSecret:
	for _, s := range encryptionSecrets {
		k, err := secrets.ToKeyState(s)
		if err == nil {
			// ignore invalid keys, check whether secret is used
			for _, us := range allUsedKeys {
				if state.EqualKeyAndEqualID(&us, &k) {
					continue NextEncryptionSecret
				}
			}
		}

		// skip the most recent unused secrets around
		if skippedKeys < keepNumberOfSecrets {
			skippedKeys++
			continue
		}

		// any secret that isn't a read key isn't used.  just delete them.
		// two phase delete: finalizer, then delete

		// remove our finalizer if it is present
		secret := s.DeepCopy()
		if finalizers := sets.NewString(secret.Finalizers...); finalizers.Has(secrets.EncryptionSecretFinalizer) {
			delete(finalizers, secrets.EncryptionSecretFinalizer)
			secret.Finalizers = finalizers.List()
			var updateErr error
			secret, updateErr = c.secretClient.Secrets("openshift-config-managed").Update(secret)
			deleteErrs = append(deleteErrs, updateErr)
			if updateErr != nil {
				continue
			}
		}

		// remove the actual secret
		if err := c.secretClient.Secrets("openshift-config-managed").Delete(secret.Name, nil); err != nil {
			deleteErrs = append(deleteErrs, err)
		} else {
			deletedKeys++
			klog.V(2).Infof("Successfully pruned secret %s/%s", secret.Namespace, secret.Name)
		}
	}
	if deletedKeys > 0 {
		recorder.Eventf("EncryptionKeysPruned", "Successfully pruned %d secrets", deletedKeys)
	}
	return utilerrors.FilterOut(utilerrors.NewAggregate(deleteErrs), errors.IsNotFound)
}
