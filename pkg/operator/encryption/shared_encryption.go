package encryption

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	apiserverconfig "k8s.io/apiserver/pkg/apis/config"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

// To understand the state machine used by the encryption controllers, we have to consider
// the states that each secret can be in.  The states are read, write, and migrated.
// Note that this refers to observed state, meaning that all API servers have observed the given
// secret at that level.  Consider when a new key is created.  Conceptually any new key can be
// used as a read key.  However, a new key is not created with the read annotation set because
// it is not officially a read key until all API servers have seen it as such.  See
// encryptionSecretReadTimestamp and related constants below.
//
// The top states refer to the observed states, i.e. states that have matching annotations.
// The other states in the diagram are conceptual states.
//
//            read     ------->     write     ------->     migrated
//             /\                     /\                      /\
//            /  \                   /  \                    /  \
//           /    \                 /    \                  /    \
//          /      \               /      \                /      \
//         /        \             /        \              /        \
//        /          \           /          \            /          \
//    created      set as write key        can be migrated        set as read key     ------->     deleted
//                 in encryption                                  in encryption
//                 config                                         config
//
// Let us consider the life of an encryption key:
//   1. A new key A is created.
//   2. Key A gets included in the encryption config secret as a read key.
//   3. That level of encryption config is seen by all API servers.
//   4. The key A is annotated as a read key.
//   5. A new encryption config secret is created with key A as a write key.
//   6. That level of encryption config is seen by all API servers.
//   7. The key A is annotated as a write key.
//   8. Now key A can be migrated to.
//   9. Storage migration is run for the resource associated with key A.
//  10. The key A is annotated as a migrated key.
//  11. A different key B is created and starts going through the same process.
//  12. Key B is observed as a read key and then as a write key.
//  13. The original key A gets included in the encryption config secret as a read key.
//  14. Key A must stay in the encryption config secret until the new key B has been migrated to.
//  15. Key B is migrated to (it follows the same path as key A).
//  16. Key A is deleted and removed from the encryption config secret.

// These labels are used to find secrets that build up the final encryption config.  The names of the
// secrets are in format <shared prefix>-<unique monotonically increasing uint> (the uint is the keyID).
// For example, openshift-kube-apiserver-core-secrets-encryption-3.  Note that other than the -3 postfix,
// the name of the secret is irrelevant since the labels are used to find the secrets.  Of course the key
// minting controller cares about the entire name since it needs to be able to create secrets for different
// resources that do not conflict.  It also needs to know when it has already created a secret for a given
// keyID meaning it cannot just use a random prefix.  As such the name must include the data that is contained
// within the labels.  Thus the format used is <component>-<group>-<resource>-encryption-<keyID>.  This keeps
// everything distinct and fully deterministic.  The keys are ordered by keyID where a smaller ID means an
// earlier key.  Thus they are listed in ascending order.  This means that the latest secret (the one with
// the largest keyID) is the current desired write key (whether it is or is not the write key depends on
// what part of that state diagram it is currently in).  Note that different resources can have overlapping
// keyIDs since they are individually tracked.  This per resource design allows us to easily add new resources
// as encryption targets (imagine if a new resource that should be encrypted is later added to the API).
const (
	encryptionSecretComponent = "encryption.operator.openshift.io/component"
	encryptionSecretGroup     = "encryption.operator.openshift.io/group"
	encryptionSecretResource  = "encryption.operator.openshift.io/resource"
)

// These annotations are used to mark the current observed state of a secret.  They correspond to the states
// described in the state machine at the top of this file.  The value associated with these keys is the
// time (in RFC3339 format) at which the state observation occurred.  Note that most sections of the code
// only check if the value is set to a non-empty value.  The exception to this is key minting controller
// which parses the migrated timestamp to determine if enough time has passed and a new key should be created.
const (
	encryptionSecretReadTimestamp     = "encryption.operator.openshift.io/read-timestamp"
	encryptionSecretWriteTimestamp    = "encryption.operator.openshift.io/write-timestamp"
	encryptionSecretMigratedTimestamp = "encryption.operator.openshift.io/migrated-timestamp"
)

// encryptionSecretMigrationInterval determines how much time must pass after a key has been observed as
// migrated before a new key is created by the key minting controller.  The new key's ID will be one
// greater than the last key's ID (the first key has a key ID of 1).
const encryptionSecretMigrationInterval = 30 * time.Minute // TODO how often?  -->  probably one month

// In the data field of the secret API object, this (map) key is used to hold the actual encryption key
// (i.e. for AES-CBC mode the value associated with this map key is 32 bytes of random noise).
const encryptionSecretKeyData = "encryption.operator.openshift.io-key"

// revisionLabel is used to find the current revision for a given API server.
const revisionLabel = "revision"

// these can only be used to encode and decode EncryptionConfiguration objects
var (
	encoder runtime.Encoder
	decoder runtime.Decoder
)

func init() {
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)
	utilruntime.Must(apiserverconfigv1.AddToScheme(scheme))
	utilruntime.Must(apiserverconfig.AddToScheme(scheme))
	encoder = codecs.LegacyCodec(apiserverconfigv1.SchemeGroupVersion)
	decoder = codecs.UniversalDecoder(apiserverconfigv1.SchemeGroupVersion)
}

type groupResourcesState map[schema.GroupResource]keysState
type keysState struct {
	keys        []apiserverconfigv1.Key
	secrets     []*corev1.Secret
	keyToSecret map[apiserverconfigv1.Key]*corev1.Secret
	secretToKey map[string]apiserverconfigv1.Key

	secretsReadYes []*corev1.Secret
	secretsReadNo  []*corev1.Secret

	secretsWriteYes []*corev1.Secret
	secretsWriteNo  []*corev1.Secret

	secretsMigratedYes []*corev1.Secret
	secretsMigratedNo  []*corev1.Secret

	lastMigrated      *corev1.Secret
	lastMigratedKey   apiserverconfigv1.Key
	lastMigratedKeyID uint64
}

type groupResourceKeys struct {
	writeKey apiserverconfigv1.Key
	readKeys []apiserverconfigv1.Key
}

func (k groupResourceKeys) hasWriteKey() bool {
	return len(k.writeKey.Name) > 0 && len(k.writeKey.Secret) > 0
}

func getEncryptionState(secretClient corev1client.SecretInterface, encryptionSecretSelector metav1.ListOptions, encryptedGRs map[schema.GroupResource]bool) (groupResourcesState, error) {
	encryptionSecretList, err := secretClient.List(encryptionSecretSelector)
	if err != nil {
		return nil, err
	}
	encryptionSecrets := encryptionSecretList.Items

	// make sure encryptionSecrets is sorted in ascending order by keyID
	// this allows us to calculate the per resource write key for the encryption config secret
	// see comment above encryptionSecretComponent near the top of this file
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		// it is fine to ignore the validKeyID bool here because we filter out invalid secrets in the next loop
		// thus it does not matter where the invalid secrets get sorted to
		// conflicting keyIDs between different resources are not an issue because we track each resource separately
		iKeyID, _ := secretToKeyID(&encryptionSecrets[i])
		jKeyID, _ := secretToKeyID(&encryptionSecrets[j])
		return iKeyID < jKeyID
	})

	encryptionState := groupResourcesState{}

	for _, es := range encryptionSecrets {
		// make sure we capture the range variable since we take the address and append it to other lists
		rangeES := es
		encryptionSecret := &rangeES

		gr, key, keyID, ok := secretToKey(encryptionSecret, encryptedGRs)
		if !ok {
			klog.Infof("skipping encryption secret %s as it has invalid data", encryptionSecret.Name)
			continue
		}

		grState := encryptionState[gr]

		// TODO figure out which lists can be dropped, maps may be better in some places

		grState.keys = append(grState.keys, key)
		grState.secrets = append(grState.secrets, encryptionSecret)

		if grState.keyToSecret == nil {
			grState.keyToSecret = map[apiserverconfigv1.Key]*corev1.Secret{}
		}
		grState.keyToSecret[key] = encryptionSecret

		if grState.secretToKey == nil {
			grState.secretToKey = map[string]apiserverconfigv1.Key{}
		}
		grState.secretToKey[encryptionSecret.Name] = key

		appendSecretPerAnnotationState(&grState.secretsReadYes, &grState.secretsReadNo, encryptionSecret, encryptionSecretReadTimestamp)
		appendSecretPerAnnotationState(&grState.secretsWriteYes, &grState.secretsWriteNo, encryptionSecret, encryptionSecretWriteTimestamp)
		appendSecretPerAnnotationState(&grState.secretsMigratedYes, &grState.secretsMigratedNo, encryptionSecret, encryptionSecretMigratedTimestamp)

		// keep overwriting the lastMigrated fields since we know that the iteration order is sorted by keyID
		if len(encryptionSecret.Annotations[encryptionSecretMigratedTimestamp]) > 0 {
			grState.lastMigrated = encryptionSecret
			grState.lastMigratedKey = key
			grState.lastMigratedKeyID = keyID
		}

		encryptionState[gr] = grState
	}

	return encryptionState, nil
}

func appendSecretPerAnnotationState(in, out *[]*corev1.Secret, secret *corev1.Secret, annotation string) {
	if len(secret.Annotations[annotation]) != 0 {
		*in = append(*in, secret)
	} else {
		*out = append(*out, secret)
	}
}

func secretToKey(encryptionSecret *corev1.Secret, validGRs map[schema.GroupResource]bool) (schema.GroupResource, apiserverconfigv1.Key, uint64, bool) {
	group := encryptionSecret.Labels[encryptionSecretGroup]
	resource := encryptionSecret.Labels[encryptionSecretResource]
	keyData := encryptionSecret.Data[encryptionSecretKeyData]

	keyID, validKeyID := secretToKeyID(encryptionSecret)

	gr := schema.GroupResource{Group: group, Resource: resource}
	key := apiserverconfigv1.Key{
		// we use keyID as the name to limit the length of the field as it is used as a prefix for every value in etcd
		Name:   strconv.FormatUint(keyID, 10),
		Secret: base64.StdEncoding.EncodeToString(keyData),
	}
	invalidKey := len(resource) == 0 || len(keyData) == 0 || !validKeyID || !validGRs[gr]

	return gr, key, keyID, !invalidKey
}

func secretToKeyID(encryptionSecret *corev1.Secret) (uint64, bool) {
	// see format and ordering comment above encryptionSecretComponent near the top of this file
	lastIdx := strings.LastIndex(encryptionSecret.Name, "-")
	keyIDStr := encryptionSecret.Name[lastIdx+1:] // this can never overflow since str[-1+1:] is always valid
	keyID, keyIDErr := strconv.ParseUint(keyIDStr, 10, 0)
	invalidKeyID := lastIdx == -1 || keyIDErr != nil
	return keyID, !invalidKeyID
}

func getResourceConfigs(encryptionState groupResourcesState) []apiserverconfigv1.ResourceConfiguration {
	resourceConfigs := make([]apiserverconfigv1.ResourceConfiguration, 0, len(encryptionState))

	for gr, grKeys := range encryptionState {
		resourceConfigs = append(resourceConfigs, apiserverconfigv1.ResourceConfiguration{
			Resources: []string{gr.String()}, // we are forced to lose data here because this API is broken
			Providers: keysToProviders(grKeys),
		})
	}

	// make sure our output is stable
	sort.Slice(resourceConfigs, func(i, j int) bool {
		return resourceConfigs[i].Resources[0] < resourceConfigs[j].Resources[0] // each resource has its own keys
	})

	return resourceConfigs
}

func grKeysToDesiredKeys(grKeys keysState) groupResourceKeys {
	desired := groupResourceKeys{}

	desired.writeKey = determineWriteKey(grKeys)

	// keys have a duplicate of the write key
	// or there is no write key

	// iterate in reverse to order the read keys with highest keyID first
	for i := len(grKeys.keys) - 1; i >= 0; i-- {
		readKey := grKeys.keys[i]
		if desired.hasWriteKey() && readKey == desired.writeKey {
			continue // if present, drop the duplicate write key from the list
		}

		desired.readKeys = append(desired.readKeys, readKey)

		if len(grKeys.secretsMigratedYes) > 0 && readKey == grKeys.lastMigratedKey {
			// we only need the read keys that have equal or higher keyID than the last migrated key
			// note that readKeys should only have one item unless there is a bug in the key minting controller
			// this also serves to limit the size of the final encryption secret (even if pruning fails)
			break
		}
	}

	return desired
}

func determineWriteKey(grKeys keysState) apiserverconfigv1.Key {
	// first write that is not migrated
	for _, writeYes := range grKeys.secretsWriteYes {
		if len(writeYes.Annotations[encryptionSecretMigratedTimestamp]) == 0 {
			return grKeys.secretToKey[writeYes.Name]
		}
	}

	// first read that is not write
	for _, readYes := range grKeys.secretsReadYes {
		if len(readYes.Annotations[encryptionSecretWriteTimestamp]) == 0 {
			return grKeys.secretToKey[readYes.Name]
		}
	}

	// no key is transitioning so just use last migrated
	if len(grKeys.secretsMigratedYes) > 0 {
		return grKeys.lastMigratedKey
	}

	// no write key
	return apiserverconfigv1.Key{}
}

func keysToProviders(grKeys keysState) []apiserverconfigv1.ProviderConfiguration {
	desired := grKeysToDesiredKeys(grKeys)

	allKeys := desired.readKeys

	// write key comes first
	if desired.hasWriteKey() {
		allKeys = append([]apiserverconfigv1.Key{desired.writeKey}, allKeys...)
	}

	aescbc := apiserverconfigv1.ProviderConfiguration{
		AESCBC: &apiserverconfigv1.AESConfiguration{
			Keys: allKeys,
		},
	}
	identity := apiserverconfigv1.ProviderConfiguration{
		Identity: &apiserverconfigv1.IdentityConfiguration{},
	}

	// assume the common case of having a write key so identity comes last
	providers := []apiserverconfigv1.ProviderConfiguration{aescbc, identity}
	// if we have no write key, identity comes first
	if !desired.hasWriteKey() {
		providers = []apiserverconfigv1.ProviderConfiguration{identity, aescbc}
	}

	return providers
}

func shouldRunEncryptionController(operatorClient operatorv1helpers.StaticPodOperatorClient) (bool, error) {
	operatorSpec, _, _, err := operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return false, err
	}

	return management.IsOperatorManaged(operatorSpec.ManagementState), nil
}

// getAPIServerRevisionOfAllInstances attempts to find the current revision that
// the API servers are running at.  If all API servers have not converged onto a
// a single revision, it returns the empty string and possibly an error.
// Converged can be defined as:
//   1. All running pods are ready and at the same revision
//   2. There are no pending or unknown pods
//   3. All succeeded and failed pods have revisions that are before the running pods
// Once a converged revision has been determined, it can be used to determine
// what encryption config state has been successfully observed by the API servers.
// It assumes that podClient is doing live lookups against the cluster state.
// TODO what if someone deletes pods?  We could see an invalid intermediate state.
func getAPIServerRevisionOfAllInstances(podClient corev1client.PodInterface) (string, error) {
	// do a live list so we never get confused about what revision we are on
	apiServerPods, err := podClient.List(metav1.ListOptions{LabelSelector: "apiserver=true"})
	if err != nil {
		return "", err
	}

	revisions := sets.NewString()
	failed := sets.NewString()

	for _, apiServerPod := range apiServerPods.Items {
		switch phase := apiServerPod.Status.Phase; phase {
		case corev1.PodRunning: // TODO check that total running == number of masters?
			if !podReady(apiServerPod) {
				return "", nil // pods are not fully ready
			}
			revisions.Insert(apiServerPod.Labels[revisionLabel])
		case corev1.PodPending:
			return "", nil // pods are not fully ready
		case corev1.PodUnknown:
			return "", fmt.Errorf("api server pod %s in unknown phase", apiServerPod.Name)
		case corev1.PodSucceeded, corev1.PodFailed:
			// handle failed pods carefully to make sure things are healthy
			// since the API server should never exit, a succeeded pod is considered as failed
			failed.Insert(apiServerPod.Labels[revisionLabel])
		default:
			// error in case new unexpected phases get added
			return "", fmt.Errorf("api server pod %s has unexpected phase %v", apiServerPod.Name, phase)
		}
	}

	if len(revisions) != 1 {
		return "", nil // api servers have not converged onto a single revision
	}
	revision, _ := revisions.PopAny()

	if failed.Has(revision) {
		return "", fmt.Errorf("api server revision %s has both running and failed pods", revision)
	}

	revisionNum, err := strconv.Atoi(revision)
	if err != nil {
		return "", fmt.Errorf("api server has invalid revision: %v", err)
	}

	for _, failedRevision := range failed.List() { // iterate in defined order
		failedRevisionNum, err := strconv.Atoi(failedRevision)
		if err != nil {
			return "", fmt.Errorf("api server has invalid failed revision: %v", err)
		}
		if failedRevisionNum > revisionNum { // TODO can this dead lock?
			return "", fmt.Errorf("api server has failed revision %v which is newer than running revision %v", failedRevisionNum, revisionNum)
		}
	}

	return revision, nil
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func getEncryptionConfig(secrets corev1client.SecretInterface, revision string) (*apiserverconfigv1.EncryptionConfiguration, error) {
	encryptionConfigSecret, err := secrets.Get(encryptionConfSecret+"-"+revision, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	encryptionConfigObj, err := runtime.Decode(decoder, encryptionConfigSecret.Data[encryptionConfSecret])
	if err != nil {
		return nil, fmt.Errorf("failed to decode encryption config at revision %s: %v", revision, err)
	}

	encryptionConfig, ok := encryptionConfigObj.(*apiserverconfigv1.EncryptionConfiguration)
	if !ok {
		return nil, fmt.Errorf("encryption config for revision %s has wrong type %T", revision, encryptionConfigObj)
	}
	return encryptionConfig, nil
}

func getGRsActualKeys(encryptionConfig *apiserverconfigv1.EncryptionConfiguration) map[schema.GroupResource]groupResourceKeys {
	out := map[schema.GroupResource]groupResourceKeys{}
	for _, resourceConfig := range encryptionConfig.Resources {
		if len(resourceConfig.Resources) == 0 || len(resourceConfig.Providers) < 2 {
			continue // should never happen
		}

		gr := schema.ParseGroupResource(resourceConfig.Resources[0])
		provider1 := resourceConfig.Providers[0]
		provider2 := resourceConfig.Providers[1]

		switch {
		case provider1.AESCBC != nil && len(provider1.AESCBC.Keys) != 0:
			out[gr] = groupResourceKeys{
				writeKey: provider1.AESCBC.Keys[0],
				readKeys: provider1.AESCBC.Keys[1:],
			}
		case provider1.Identity != nil && provider2.AESCBC != nil && len(provider2.AESCBC.Keys) != 0:
			out[gr] = groupResourceKeys{
				readKeys: provider2.AESCBC.Keys,
			}
		}
	}
	return out
}

// setTimestampAnnotationIfNotSet will set the value of the given annotation annotation
// to the current time if it is not already set.  This serves to mark the given secret
// as having transitioned into a certain state at a specific time.
func setTimestampAnnotationIfNotSet(secretClient corev1client.SecretsGetter, recorder events.Recorder, secret *corev1.Secret, annotation string) error {
	if len(secret.Annotations[annotation]) != 0 {
		return nil
	}
	secret = secret.DeepCopy()

	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[annotation] = time.Now().Format(time.RFC3339)

	_, _, updateErr := resourceapply.ApplySecret(secretClient, recorder, secret)
	return updateErr // let conflict errors cause a retry
}

func setUpGlobalMachineConfigEncryptionInformers(
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	eventHandler cache.ResourceEventHandler,
) []cache.InformerSynced {
	operatorInformer := operatorClient.Informer()
	operatorInformer.AddEventHandler(eventHandler)

	secretsInformer := kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer()
	secretsInformer.AddEventHandler(eventHandler)

	return []cache.InformerSynced{
		operatorInformer.HasSynced,
		secretsInformer.HasSynced,
	}
}

func setUpAllEncryptionInformers(
	operatorClient operatorv1helpers.StaticPodOperatorClient,
	targetNamespace string,
	kubeInformersForNamespaces operatorv1helpers.KubeInformersForNamespaces,
	eventHandler cache.ResourceEventHandler,
) []cache.InformerSynced {
	podInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Pods().Informer()
	podInformer.AddEventHandler(eventHandler)

	secretsInformer := kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer()
	secretsInformer.AddEventHandler(eventHandler)

	return append([]cache.InformerSynced{
		podInformer.HasSynced,
		secretsInformer.HasSynced,
	},
		setUpGlobalMachineConfigEncryptionInformers(operatorClient, kubeInformersForNamespaces, eventHandler)...)

}

// groupToHumanReadable extracts a group from gr and makes it more readable, for example it converts an empty group to "core"
// Note: do not use it to get resources from the server only when printing to a log file
func groupToHumanReadable(gr schema.GroupResource) string {
	group := gr.Group
	if len(group) == 0 {
		group = "core"
	}
	return group
}

func getEncryptionConfigAndState(
	podClient corev1client.PodInterface,
	secretClient corev1client.SecretsGetter,
	targetNamespace string,
	encryptionSecretSelector metav1.ListOptions,
	encryptedGRs map[schema.GroupResource]bool,
) (*apiserverconfigv1.EncryptionConfiguration, groupResourcesState, string, error) {
	revision, err := getAPIServerRevisionOfAllInstances(podClient)
	if err != nil {
		return nil, nil, "", err
	}
	if len(revision) == 0 {
		return nil, nil, "APIServerRevisionNotConverged", nil
	}

	encryptionConfig, err := getEncryptionConfig(secretClient.Secrets(targetNamespace), revision)
	if err != nil {
		return nil, nil, "", err
	}

	encryptionState, err := getEncryptionState(secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace), encryptionSecretSelector, encryptedGRs)
	if err != nil {
		return nil, nil, "", err
	}

	return encryptionConfig, encryptionState, "", nil
}
