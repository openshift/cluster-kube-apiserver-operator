package encryption

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
)

// These annotations are used to mark the current observed state of a secret.  They correspond to the states
// described in the state machine at the top of this file.  The value associated with these keys is the
// time (in RFC3339 format) at which the state observation occurred.  Note that most sections of the code
// only check if the value is set to a non-empty value.  The exception to this is key minting controller
// which parses the migrated timestamp to determine if enough time has passed and a new key should be created.
const (
	encryptionSecretMigratedTimestamp = "encryption.operator.openshift.io/migrated-timestamp"
	encryptionSecretMigratedResources = "encryption.operator.openshift.io/migrated-resources"
)

// encryptionSecretMode is the annotation that determines how the provider associated with a given key is
// configured.  For example, a key could be used with AES-CBC or Secretbox.  This allows for algorithm
// agility.  When the default mode used by the key minting controller changes, it will force the creation
// of a new key under the new mode even if encryptionSecretMigrationInterval has not been reached.
const encryptionSecretMode = "encryption.operator.openshift.io/mode"

// encryptionSecretInternalReason is the annotation that denotes why a particular key
// was created based on "internal" reasons (i.e. key minting controller decided a new
// key was needed for some reason X).  It is tracked solely for the purposes of debugging.
const encryptionSecretInternalReason = "encryption.operator.openshift.io/internal-reason"

// encryptionSecretExternalReason is the annotation that denotes why a particular key was created based on
// "external" reasons (i.e. force key rotation for some reason Y).  It allows the key minting controller to
// determine if a new key should be created even if encryptionSecretMigrationInterval has not been reached.
const encryptionSecretExternalReason = "encryption.operator.openshift.io/external-reason"

// encryptionSecretFinalizer is a finalizer attached to all secrets generated
// by the encryption controllers.  Its sole purpose is to prevent the accidental
// deletion of secrets by enforcing a two phase delete.
const encryptionSecretFinalizer = "encryption.operator.openshift.io/deletion-protection"

// encryptionSecretMigrationInterval determines how much time must pass after a key has been observed as
// migrated before a new key is created by the key minting controller.  The new key's ID will be one
// greater than the last key's ID (the first key has a key ID of 1).
const encryptionSecretMigrationInterval = 30 * time.Minute // TODO how often?  -->  probably one week

// In the data field of the secret API object, this (map) key is used to hold the actual encryption key
// (i.e. for AES-CBC mode the value associated with this map key is 32 bytes of random noise).
const encryptionSecretKeyData = "encryption.operator.openshift.io-key"

// These annotations try to scare anyone away from editing the encryption secrets.  It is trivial for
// an external actor to break the invariants of the state machine and render the cluster unrecoverable.
const (
	kubernetesDescriptionKey        = "kubernetes.io/description"
	kubernetesDescriptionScaryValue = `WARNING: DO NOT EDIT.
Altering of the encryption secrets will render you cluster inaccessible.
Catastrophic data loss can occur from the most minor changes.`
)

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

// TODO docs
type groupResourcesState map[schema.GroupResource]keysState
type keysState struct {
	targetNamespace string

	readSecrets []*corev1.Secret
	writeSecret *corev1.Secret
}

func (k keysState) ReadKeys() []keyAndMode {
	ret := []keyAndMode{}
	for _, readKey := range k.readSecrets {
		keyAndMode, _, ok := secretToKey(readKey, k.targetNamespace)
		if !ok {
			continue
			// TODO question life choices
		}
		ret = append(ret, keyAndMode)
	}
	return ret
}

func (k keysState) WriteKey() keyAndMode {
	if k.writeSecret == nil {
		return keyAndMode{mode: identity}
	}

	writeKeyAndMode, _, ok := secretToKey(k.writeSecret, k.targetNamespace)
	if !ok {
		// TODO question life choices
		return keyAndMode{mode: identity}
	}

	return writeKeyAndMode
}

func (k keysState) LatestKeyID() (uint64, bool) {
	return secretToKeyID(k.readSecrets[0])
}

// TODO docs
type groupResourceKeys struct {
	writeKey keyAndMode
	readKeys []keyAndMode
}

func (k groupResourceKeys) hasWriteKey() bool {
	return len(k.writeKey.key.Name) > 0 && len(k.writeKey.key.Secret) > 0
}

type keyAndMode struct {
	key  apiserverconfigv1.Key
	mode mode
}

// mode is the value associated with the encryptionSecretMode annotation
type mode string

// The current set of modes that are supported along with the default mode that is used.
// These values are encoded into the secret and thus must not be changed.
// Strings are used over iota because they are easier for a human to understand.
const (
	aescbc    mode = "aescbc"    // available from the first release, see defaultMode below
	secretbox mode = "secretbox" // available from the first release, see defaultMode below
	identity  mode = "identity"  // available from the first release, see defaultMode below

	// Changing this value requires caution to not break downgrades.
	// Specifically, if some new mode is released in version X, that new mode cannot
	// be used as the defaultMode until version X+1.  Thus on a downgrade the operator
	// from version X will still be able to honor the observed encryption state
	// (and it will do a key rotation to force the use of the old defaultMode).
	defaultMode = aescbc // TODO change to identity (to default to off) on first release
)

var modeToNewKeyFunc = map[mode]func() []byte{
	aescbc:    newAES256Key,
	secretbox: newAES256Key, // secretbox requires a 32 byte key so we can reuse the same function here
	identity:  newIdentityKey,
}

func newAES256Key() []byte {
	b := make([]byte, 32) // AES-256 == 32 byte key
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func newIdentityKey() []byte {
	return make([]byte, 16) // the key is not used to perform encryption but must be a valid AES key
}

var emptyStaticIdentityKey = base64.StdEncoding.EncodeToString(newIdentityKey())

func getDesiredEncryptionStateFromClients(targetNamespace string, podClient corev1client.PodsGetter, secretClient corev1client.SecretsGetter, encryptionSecretSelector metav1.ListOptions, encryptedGRs map[schema.GroupResource]bool) (groupResourcesState, error) {
	revision, err := getAPIServerRevisionOfAllInstances(podClient.Pods(targetNamespace))
	if err != nil {
		return groupResourcesState{}, err
	}
	if len(revision) == 0 {
		return groupResourcesState{}, err
	}

	encryptionConfig, err := getCurrentEncryptionConfig(secretClient.Secrets(targetNamespace), revision)
	if err != nil {
		return groupResourcesState{}, err
	}

	return getDesiredEncryptionState(encryptionConfig, targetNamespace, secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace), encryptionSecretSelector, encryptedGRs)
}

// getDesiredEncryptionState returns the desired state of encryption for all resources.  To do this it compares the current state
// against the available secrets.
// the encryptionConfig describes the actual state of every GroupResource.  We compare this against requested group resources because
// we may be missing one.
// the basic rules are:
// 1. every requested groupresource must honor every available key before any write key is changed.
// 2. Once every resources honors every key, the write key should be the latest key available
// 3. Once every resources honors the same write key AND that write key has migrated every request resource, all non-write keys should be removed.
func getDesiredEncryptionState(encryptionConfig *apiserverconfigv1.EncryptionConfiguration, targetNamespace string, secretClient corev1client.SecretInterface, encryptionSecretSelector metav1.ListOptions, encryptedGRs map[schema.GroupResource]bool) (groupResourcesState, error) {
	encryptionSecretList, err := secretClient.List(encryptionSecretSelector)
	if err != nil {
		return nil, err
	}
	encryptionSecrets := []*corev1.Secret{}
	for _, item := range encryptionSecretList.Items {
		encryptionSecrets = append(encryptionSecrets, item.DeepCopy())
	}

	// make sure encryptionSecrets is sorted in DESCENDING order by keyID
	// this allows us to calculate the per resource write key as the first item and recent keys are first
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		// it is fine to ignore the validKeyID bool here because we filter out invalid secrets in the next loop
		// thus it does not matter where the invalid secrets get sorted to
		// conflicting keyIDs between different resources are not an issue because we track each resource separately
		iKeyID, _ := secretToKeyID(encryptionSecrets[i])
		jKeyID, _ := secretToKeyID(encryptionSecrets[j])
		return iKeyID > jKeyID
	})

	// this is our output from the for loop below
	encryptionState := groupResourcesState{}

	resourcesToEncryptionKeys := getGRsActualKeys(encryptionConfig)
	for gr, keys := range resourcesToEncryptionKeys {
		writeSecret := findSecretForKey(keys.writeKey.key.Secret, encryptionSecrets)
		readSecrets := []*corev1.Secret{}
		for _, readKey := range keys.readKeys {
			readSecret := findSecretForKey(readKey.key.Secret, encryptionSecrets)
			readSecrets = append(readSecrets, readSecret)
		}

		encryptionState[gr] = keysState{
			targetNamespace: targetNamespace,
			writeSecret:     writeSecret,
			readSecrets:     readSecrets,
		}
	}

	// see if any resource is missing and properly reflect that state so that we will add the read keys and take it through the
	// transition correctly.
	for gr := range encryptedGRs {
		if _, ok := encryptionState[gr]; !ok {
			encryptionState[gr] = keysState{
				targetNamespace: targetNamespace,
			}
		}
	}

	// TODO allow removing resources. This would require transitioning back to identity.  For now, because we work against a single
	// key, we can simply keep moving that additional resource even in the face of downgrades.

	allReadSecretsAsExpected := true
	for _, grState := range encryptionState {
		match := true
		if len(grState.readSecrets) != len(encryptionSecrets) {
			allReadSecretsAsExpected = false
			break
		}
		for i := range encryptionSecrets {
			if grState.readSecrets[i].Name != encryptionSecrets[i].Name {
				match = false
				break
			}
		}
		if match == false {
			allReadSecretsAsExpected = false
			break
		}
	}
	// if our read secrets aren't all the same, the first thing to do is to force all the read secrets and wait for stability
	if !allReadSecretsAsExpected {
		for _, grState := range encryptionState {
			grState.readSecrets = encryptionSecrets
		}
		return encryptionState, nil
	}

	// we have consistent and completely current read secrets, the next step is determining a consistent write key.
	// To do this, we will choose the most current write key and use that to write the data
	allWriteSecretsAsExpected := true
	for _, grState := range encryptionState {
		if grState.writeSecret.Name != encryptionSecrets[0].Name {
			allWriteSecretsAsExpected = false
			break
		}
	}
	// if our write secrets aren't all the same, update all the write secrets and wait for stability.  We can move write keys
	// before all the data has been migrated
	if !allWriteSecretsAsExpected {
		for _, grState := range encryptionState {
			grState.writeSecret = encryptionSecrets[0]
		}
		return encryptionState, nil
	}

	// at this point all of our read and write secrets are the same.  We need to inspect the write secret to ensure that it has
	// all the expect resources listed as having been migrated.  If this is true, then we can prune the read secrets down
	// to a single entry.

	// get a list of all the resources we have, so that we can compare against the migrated keys annotation.
	allResources := []schema.GroupResource{}
	for gr := range encryptionState {
		allResources = append(allResources, gr)
	}

	migratedResourceString := encryptionSecrets[0].Annotations[encryptionSecretMigratedResources]
	// if no migration has happened, we need to wait until it has
	if len(migratedResourceString) == 0 {
		return encryptionState, nil
	}
	migratedResources := &GroupResources{}
	// if we cannot read the data, wait until we can
	if err := json.Unmarshal([]byte(migratedResourceString), migratedResources); err != nil {
		return encryptionState, nil
	}

	foundCount := 0
	for _, expected := range allResources {
		for _, actual := range migratedResources.Resources {
			if expected == actual {
				foundCount++
				break
			}
		}
	}
	// if we did not find migration indications for all resources, then just wait until we do
	if foundCount != len(allResources) {
		return encryptionState, nil
	}

	// if we have migrated all of our resources, the next step is remove all unnecessary read keys.  We only need the write
	// key now
	for _, grState := range encryptionState {
		grState.readSecrets = []*corev1.Secret{encryptionSecrets[0]}
	}
	return encryptionState, nil
}

type GroupResources struct {
	Resources []schema.GroupResource `json:"resources"`
}

func findSecretForKey(key string, secrets []*corev1.Secret) *corev1.Secret {
	for _, secret := range secrets {
		if string(secret.Data[encryptionSecretKeyData]) == key {
			return secret.DeepCopy()
		}
	}
	return nil
}

func findSecretForKeyWithClient(key string, secretClient corev1client.SecretsGetter, encryptionSecretSelector metav1.ListOptions) (*corev1.Secret, error) {
	encryptionSecretList, err := secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(encryptionSecretSelector)
	if err != nil {
		return nil, err
	}

	for _, secret := range encryptionSecretList.Items {
		if string(secret.Data[encryptionSecretKeyData]) == key {
			return secret.DeepCopy(), nil
		}
	}
	return nil, nil
}

func secretToKey(encryptionSecret *corev1.Secret, targetNamespace string) (keyAndMode, uint64, bool) {
	component := encryptionSecret.Labels[encryptionSecretComponent]
	keyData := encryptionSecret.Data[encryptionSecretKeyData]
	keyMode := mode(encryptionSecret.Annotations[encryptionSecretMode])

	keyID, validKeyID := secretToKeyID(encryptionSecret)

	key := keyAndMode{
		key: apiserverconfigv1.Key{
			// we use keyID as the name to limit the length of the field as it is used as a prefix for every value in etcd
			Name:   strconv.FormatUint(keyID, 10),
			Secret: base64.StdEncoding.EncodeToString(keyData),
		},
		mode: keyMode,
	}
	invalidKey := len(keyData) == 0 || !validKeyID || component != targetNamespace
	switch keyMode {
	case aescbc, secretbox, identity:
	default:
		invalidKey = true
	}

	return key, keyID, !invalidKey
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

// TODO docs
func grKeysToDesiredKeys(grKeys keysState) groupResourceKeys {
	desired := groupResourceKeys{}

	desired.writeKey = grKeys.WriteKey()

	// keys have a duplicate of the write key
	// or there is no write key

	readKeys := grKeys.ReadKeys()
	for i := range readKeys {
		readKey := readKeys[i]

		readKeyIsWriteKey := desired.hasWriteKey() && readKey == desired.writeKey
		// if present, do not include a duplicate write key in the read key list
		if !readKeyIsWriteKey {
			desired.readKeys = append(desired.readKeys, readKey)
		}
	}

	return desired
}

// TODO docs
func keysToProviders(grKeys keysState) []apiserverconfigv1.ProviderConfiguration {
	desired := grKeysToDesiredKeys(grKeys)

	allKeys := desired.readKeys

	// write key comes first
	if desired.hasWriteKey() {
		allKeys = append([]keyAndMode{desired.writeKey}, allKeys...)
	}

	providers := make([]apiserverconfigv1.ProviderConfiguration, 0, len(allKeys)+1) // one extra for identity

	// having identity as a key is problematic because IdentityConfiguration cannot store any data.
	// we need to be able to trace back to the secret so that it can move through the key state machine.
	// thus in this case we create a fake AES-GCM config and include that at the very end of our providers.
	// its null key will never be used to encrypt data but it will be able to move through the observed states.
	// we guarantee it is never used by making sure that the IdentityConfiguration is always ahead of it.
	var hasIdentityAsWriteKey, needsFakeIdentityProvider bool
	var fakeIdentityProvider apiserverconfigv1.ProviderConfiguration

	for i, key := range allKeys {
		switch key.mode {
		case aescbc:
			providers = append(providers, apiserverconfigv1.ProviderConfiguration{
				AESCBC: &apiserverconfigv1.AESConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			})
		case secretbox:
			providers = append(providers, apiserverconfigv1.ProviderConfiguration{
				Secretbox: &apiserverconfigv1.SecretboxConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			})
		case identity:
			// we can only track one fake identity provider
			// this is not an issue because all identity providers are conceptually equivalent
			// because they all lead to the same outcome (read and write unencrypted data)
			if needsFakeIdentityProvider {
				continue
			}
			needsFakeIdentityProvider = true
			hasIdentityAsWriteKey = i == 0
			fakeIdentityProvider = apiserverconfigv1.ProviderConfiguration{
				AESGCM: &apiserverconfigv1.AESConfiguration{
					Keys: []apiserverconfigv1.Key{key.key},
				},
			}
		default:
			// this should never happen because our input should always be valid
			klog.Infof("skipping key %s as it has invalid mode %s", key.key.Name, key.mode)
		}
	}

	identityProvider := apiserverconfigv1.ProviderConfiguration{
		Identity: &apiserverconfigv1.IdentityConfiguration{},
	}

	if desired.hasWriteKey() && !hasIdentityAsWriteKey {
		// the common case is that we have a write key, identity comes last
		providers = append(providers, identityProvider)
	} else {
		// if we have no write key, identity comes first
		providers = append([]apiserverconfigv1.ProviderConfiguration{identityProvider}, providers...)
	}

	if needsFakeIdentityProvider {
		providers = append(providers, fakeIdentityProvider)
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

func getCurrentEncryptionConfig(secrets corev1client.SecretInterface, revision string) (*apiserverconfigv1.EncryptionConfiguration, error) {
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

// TODO docs
func getGRsActualKeys(encryptionConfig *apiserverconfigv1.EncryptionConfiguration) map[schema.GroupResource]groupResourceKeys {
	out := map[schema.GroupResource]groupResourceKeys{}
	for _, resourceConfig := range encryptionConfig.Resources {
		// resources should be a single group resource and
		// providers should be have at least one "key" provider and the identity provider
		if len(resourceConfig.Resources) != 1 || len(resourceConfig.Providers) < 2 {
			klog.Infof("skipping invalid encryption config for resource %s", resourceConfig.Resources)
			continue // should never happen
		}

		grk := groupResourceKeys{}

		// we know that this is safe because providers is non-empty
		// we need to track the last provider as it may be a fake provider that is holding the identity key info
		lastIndex := len(resourceConfig.Providers) - 1
		lastProvider := resourceConfig.Providers[lastIndex]
		var hasFakeIdentityProvider bool

		for i, provider := range resourceConfig.Providers {
			var key keyAndMode

			switch {
			case provider.AESCBC != nil && len(provider.AESCBC.Keys) == 1:
				key = keyAndMode{
					key:  provider.AESCBC.Keys[0],
					mode: aescbc,
				}

			case provider.Secretbox != nil && len(provider.Secretbox.Keys) == 1:
				key = keyAndMode{
					key:  provider.Secretbox.Keys[0],
					mode: secretbox,
				}

			case provider.Identity != nil:
				if i != 0 {
					continue // we do not want to add a key for this unless it is a write key
				}
				key = keyAndMode{
					mode: identity,
				}

			case i == lastIndex && provider.AESGCM != nil && len(provider.AESGCM.Keys) == 1 && provider.AESGCM.Keys[0].Secret == emptyStaticIdentityKey:
				hasFakeIdentityProvider = true
				continue // we handle the fake identity provider at the end based on if it is read or write key

			default:
				klog.Infof("skipping invalid provider index %d for resource %s", i, resourceConfig.Resources[0])
				continue // should never happen
			}

			if i == 0 {
				grk.writeKey = key
			} else {
				grk.readKeys = append(grk.readKeys, key)
			}
		}

		// now we can explicitly handle the fake identity provider based on the key state
		switch {
		case grk.writeKey.mode == identity && hasFakeIdentityProvider:
			grk.writeKey.key = lastProvider.AESGCM.Keys[0]
		case hasFakeIdentityProvider:
			grk.readKeys = append(grk.readKeys,
				keyAndMode{
					key:  lastProvider.AESGCM.Keys[0],
					mode: identity,
				})
		}

		out[schema.ParseGroupResource(resourceConfig.Resources[0])] = grk
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

	encryptionConfig, err := getCurrentEncryptionConfig(secretClient.Secrets(targetNamespace), revision)
	if err != nil {
		return nil, nil, "", err
	}

	desiredEncryptionState, err := getDesiredEncryptionState(encryptionConfig, targetNamespace, secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace), encryptionSecretSelector, encryptedGRs)
	if err != nil {
		return nil, nil, "", err
	}

	return encryptionConfig, desiredEncryptionState, "", nil
}
