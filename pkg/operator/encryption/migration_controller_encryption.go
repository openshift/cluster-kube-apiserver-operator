package encryption

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/pager"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const migrationWorkKey = "key"

// encryptionMigrationController determines if the current write key for a given
// resource needs migration.  It waits until all API servers have converged onto
// a stable revision before making any checks.  It traces each write key back to
// the containing secret.  If that secret is not marked as migrated, a storage
// migration is run for the targeted resource.  A storage migration is simply a
// set of no-op writes for all instances of the resource.  These writes cause the
// API server to rewrite data using the latest encryption key.  If the migration
// is successful, the secret is marked as migrated with an accompanying timestamp.
// This controller effectively observes transitions from "write" to "migrated."
type encryptionMigrationController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient

	queue         workqueue.RateLimitingInterface
	eventRecorder events.Recorder

	preRunCachesSynced []cache.InformerSynced

	encryptedGRs map[schema.GroupResource]bool

	targetNamespace          string
	encryptionSecretSelector metav1.ListOptions

	secretClient corev1client.SecretsGetter

	podClient corev1client.PodInterface

	dynamicClient   dynamic.Interface
	discoveryClient discovery.ServerResourcesInterface
}

func newEncryptionMigrationController(
	targetNamespace string,
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	secretClient corev1client.SecretsGetter,
	encryptionSecretSelector metav1.ListOptions,
	eventRecorder events.Recorder,
	encryptedGRs map[schema.GroupResource]bool,
	podClient corev1client.PodInterface,
	dynamicClient dynamic.Interface, // temporary hack
	discoveryClient discovery.ServerResourcesInterface,
) *encryptionMigrationController {
	c := &encryptionMigrationController{
		operatorClient: operatorClient,

		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "EncryptionMigrationController"),
		eventRecorder: eventRecorder.WithComponentSuffix("encryption-migration-controller"),

		encryptedGRs:    encryptedGRs,
		targetNamespace: targetNamespace,

		encryptionSecretSelector: encryptionSecretSelector,
		secretClient:             secretClient,
		podClient:                podClient,
		dynamicClient:            dynamicClient,
		discoveryClient:          discoveryClient,
	}

	c.preRunCachesSynced = setUpAllEncryptionInformers(operatorClient, targetNamespace, kubeInformersForNamespaces, c.eventHandler())

	return c
}

func (c *encryptionMigrationController) sync() error {
	if ready, err := shouldRunEncryptionController(c.operatorClient); err != nil || !ready {
		return err // we will get re-kicked when the operator status updates
	}

	configError, isProgressing := c.handleEncryptionMigration()

	// update failing condition
	degraded := operatorv1.OperatorCondition{
		Type:   "EncryptionMigrationControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if configError != nil {
		degraded.Status = operatorv1.ConditionTrue
		degraded.Reason = "Error"
		degraded.Message = configError.Error()
	}

	// update progressing condition
	progressing := operatorv1.OperatorCondition{
		Type:   "EncryptionMigrationControllerProgressing",
		Status: operatorv1.ConditionFalse,
	}
	if configError == nil && isProgressing { // TODO need to think this logic through
		progressing.Status = operatorv1.ConditionTrue
		progressing.Reason = "StorageMigration"
		progressing.Message = "" // TODO maybe put job information
	}

	if _, _, updateError := operatorv1helpers.UpdateStaticPodStatus(c.operatorClient,
		operatorv1helpers.UpdateStaticPodConditionFn(degraded),
		operatorv1helpers.UpdateStaticPodConditionFn(progressing),
	); updateError != nil {
		return updateError
	}

	if isProgressing && configError == nil {
		c.queue.AddAfter(migrationWorkKey, 2*time.Minute)
	}

	return configError
}

func (c *encryptionMigrationController) handleEncryptionMigration() (error, bool) {
	// no storage migration during revision changes
	revision, err := getAPIServerRevisionOfAllInstances(c.podClient)
	if err != nil || len(revision) == 0 {
		return err, err == nil
	}

	encryptionConfig, err := getEncryptionConfig(c.secretClient.Secrets(c.targetNamespace), revision)
	if err != nil {
		return err, false
	}

	encryptionState, err := getEncryptionState(c.secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace), c.encryptionSecretSelector, c.encryptedGRs)
	if err != nil {
		return err, false
	}

	// TODO we need this check?  Could it dead lock?
	// no storage migration until all masters catch up with revision
	if !reflect.DeepEqual(encryptionConfig.Resources, getResourceConfigs(encryptionState)) {
		return fmt.Errorf("resource config not in sync"), false // TODO maybe synthetic retry
	}

	// now we can attempt migration
	var errs []error
	for gr, grActualKeys := range getGRsActualKeys(encryptionConfig) {
		if !grActualKeys.hasWriteKey() {
			continue // no write key to migrate to
		}

		writeSecret, ok := encryptionState[gr].keyToSecret[grActualKeys.writeKey]
		if !ok || len(writeSecret.Annotations[encryptionSecretMigratedTimestamp]) != 0 {
			continue // no migration needed
		}

		migrationErr := c.runStorageMigration(gr)
		errs = append(errs, migrationErr)
		if migrationErr != nil {
			continue
		}

		errs = append(errs, setTimestampAnnotationIfNotSet(c.secretClient, c.eventRecorder, writeSecret, encryptionSecretMigratedTimestamp))
	}
	return utilerrors.NewAggregate(errs), false
}

func (c *encryptionMigrationController) runStorageMigration(gr schema.GroupResource) error {
	version, err := c.getVersion(gr)
	if err != nil {
		return err
	}
	d := c.dynamicClient.Resource(gr.WithVersion(version))

	var errs []error

	listPager := pager.New(pager.SimplePageFunc(func(opts metav1.ListOptions) (runtime.Object, error) {
		allResource, err := d.List(opts)
		if err != nil {
			return nil, err // TODO this can wedge on resource expired errors with large overall list
		}
		for _, obj := range allResource.Items { // TODO parallelize for-loop
			_, updateErr := d.Namespace(obj.GetNamespace()).Update(&obj, metav1.UpdateOptions{})
			errs = append(errs, updateErr)
		}
		return &unstructured.UnstructuredList{}, nil // do not accumulate list, this fakes the visitor pattern
	}))

	listPager.FullListIfExpired = false // prevent memory explosion from full list
	_, listErr := listPager.List(context.TODO(), metav1.ListOptions{})
	errs = append(errs, listErr)

	return utilerrors.FilterOut(utilerrors.NewAggregate(errs), errors.IsNotFound, errors.IsConflict)
}

func (c *encryptionMigrationController) getVersion(gr schema.GroupResource) (string, error) {
	resourceLists, discoveryErr := c.discoveryClient.ServerPreferredResources() // safe to ignore error
	for _, resourceList := range resourceLists {
		for _, resource := range resourceList.APIResources {
			if resource.Group == gr.Group && resource.Name == gr.Resource {
				if len(resource.Version) > 0 {
					return resource.Version, nil
				}
				groupVersion, err := schema.ParseGroupVersion(resourceList.GroupVersion)
				if err == nil {
					return groupVersion.Version, nil
				}
			}
		}
	}
	return "", fmt.Errorf("failed to find version for %s, discoveryErr=%v", gr, discoveryErr)
}

func (c *encryptionMigrationController) run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting EncryptionMigrationController")
	defer klog.Infof("Shutting down EncryptionMigrationController")
	if !cache.WaitForCacheSync(stopCh, c.preRunCachesSynced...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// only start one worker
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *encryptionMigrationController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *encryptionMigrationController) processNextWorkItem() bool {
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

func (c *encryptionMigrationController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(migrationWorkKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(migrationWorkKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(migrationWorkKey) },
	}
}
