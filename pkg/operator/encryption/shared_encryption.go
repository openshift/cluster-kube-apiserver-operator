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
	"k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/openshift/library-go/pkg/operator/management"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

// This label is used to find secrets that build up the final encryption config.  The names of the
// secrets are in format <shared prefix>-<unique monotonically increasing uint> (the uint is the keyID).
// For example, openshift-kube-apiserver-encryption-3.  Note that other than the -3 postfix, the name of
// the secret is irrelevant since the label is used to find the secrets.  Of course the key minting
// controller cares about the entire name since it needs to know when it has already created a secret for a given
// keyID meaning it cannot just use a random prefix.  As such the name must include the data that is contained
// within the label.  Thus the format used is <component>-encryption-<keyID>.  This keeps everything distinct
// and fully deterministic.  The keys are ordered by keyID where a smaller ID means an earlier key.
// This means that the latest secret (the one with the largest keyID) is the current desired write key.
const encryptionSecretComponent = "encryption.apiserver.operator.openshift.io/component"

// These annotations are used to mark the current observed state of a secret.
const (
	// The time (in RFC3339 format) at which the migrated state observation occurred.  The key minting
	// controller parses this field to determine if enough time has passed and a new key should be created.
	encryptionSecretMigratedTimestamp = "encryption.apiserver.operator.openshift.io/migrated-timestamp"
	// The list of resources that were migrated when encryptionSecretMigratedTimestamp was set.
	// See the migratedGroupResources struct below to understand the JSON encoding used.
	encryptionSecretMigratedResources = "encryption.apiserver.operator.openshift.io/migrated-resources"
)

// encryptionSecretMode is the annotation that determines how the provider associated with a given key is
// configured.  For example, a key could be used with AES-CBC or Secretbox.  This allows for algorithm
// agility.  When the default mode used by the key minting controller changes, it will force the creation
// of a new key under the new mode even if encryptionSecretMigrationInterval has not been reached.
const encryptionSecretMode = "encryption.apiserver.operator.openshift.io/mode"

// encryptionSecretInternalReason is the annotation that denotes why a particular key
// was created based on "internal" reasons (i.e. key minting controller decided a new
// key was needed for some reason X).  It is tracked solely for the purposes of debugging.
const encryptionSecretInternalReason = "encryption.apiserver.operator.openshift.io/internal-reason"

// encryptionSecretExternalReason is the annotation that denotes why a particular key was created based on
// "external" reasons (i.e. force key rotation for some reason Y).  It allows the key minting controller to
// determine if a new key should be created even if encryptionSecretMigrationInterval has not been reached.
const encryptionSecretExternalReason = "encryption.apiserver.operator.openshift.io/external-reason"

// encryptionSecretFinalizer is a finalizer attached to all secrets generated
// by the encryption controllers.  Its sole purpose is to prevent the accidental
// deletion of secrets by enforcing a two phase delete.
const encryptionSecretFinalizer = "encryption.apiserver.operator.openshift.io/deletion-protection"

// encryptionSecretMigrationInterval determines how much time must pass after a key has been observed as
// migrated before a new key is created by the key minting controller.  The new key's ID will be one
// greater than the last key's ID (the first key has a key ID of 1).
const encryptionSecretMigrationInterval = time.Hour * 24 * 7 // one week

// In the data field of the secret API object, this (map) key is used to hold the actual encryption key
// (i.e. for AES-CBC mode the value associated with this map key is 32 bytes of random noise).
const encryptionSecretKeyData = "encryption.apiserver.operator.openshift.io-key"

// These annotations try to scare anyone away from editing the encryption secrets.  It is trivial for
// an external actor to break the invariants of the state machine and render the cluster unrecoverable.
const (
	kubernetesDescriptionKey        = "kubernetes.io/description"
	kubernetesDescriptionScaryValue = `WARNING: DO NOT EDIT.
Altering of the encryption secrets will render you cluster inaccessible.
Catastrophic data loss can occur from the most minor changes.`
)

// encryptionConfSecret is the name of the final encryption config secret that is revisioned per apiserver rollout.
// it also serves as the (map) key that is used to store the raw bytes of the final encryption config.
const encryptionConfSecret = "encryption-config"

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

// groupResourcesState represents the secrets (i.e. encryption keys) associated with each group resource.
// see getDesiredEncryptionState for how this map is built.  it is first fed the current state based on the on
// disk configuration.  the actual state of the secrets in the kube API is then layered on top to determine the
// overall desired configuration (which is the same as the current state when the system is at steady state).
type groupResourcesState map[schema.GroupResource]keysState
type keysState struct {
	targetNamespace string

	readSecrets []*corev1.Secret
	writeSecret *corev1.Secret
}

func (k keysState) readKeys() []keyAndMode {
	ret := make([]keyAndMode, 0, len(k.readSecrets))
	for _, readKey := range k.readSecrets {
		readKeyAndMode, _, ok := secretToKeyAndMode(readKey, k.targetNamespace)
		if !ok {
			klog.Infof("failed to convert read secret %s to key", readKey.Name)
			continue
		}
		ret = append(ret, readKeyAndMode)
	}
	return ret
}

func (k keysState) writeKey() keyAndMode {
	if k.writeSecret == nil {
		return keyAndMode{}
	}

	writeKeyAndMode, _, ok := secretToKeyAndMode(k.writeSecret, k.targetNamespace)
	if !ok {
		klog.Infof("failed to convert write secret %s to key", k.writeSecret.Name)
		return keyAndMode{}
	}

	return writeKeyAndMode
}

func (k keysState) latestKey() (*corev1.Secret, uint64) {
	key := k.readSecrets[0]
	keyID, _ := secretToKeyID(key)
	return key, keyID
}

// groupResourceKeys represents, for a single group resource, the write and read keys in a
// format that can be directly translated to and from the on disk EncryptionConfiguration object.
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
	defaultMode = identity // we default to encryption being disabled for now
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

// getDesiredEncryptionState returns the desired state of encryption for all resources.
// To do this it compares the current state against the available secrets.
// The encryptionConfig describes the actual state of every GroupResource.
// We compare this against requested group resources because we may be missing one.
// The basic rules are:
//   1. every requested group resource must honor every available key before any write key is changed.
//   2. Once every resource honors every key, the write key should be the latest key available
//   3. Once every resource honors the same write key AND that write key has migrated every requested resource,
//      all non-write keys should be removed.
// TODO unit tests
func getDesiredEncryptionState(encryptionConfig *apiserverconfigv1.EncryptionConfiguration, targetNamespace string, encryptionSecretList *corev1.SecretList, encryptedGRs map[schema.GroupResource]bool) groupResourcesState {
	encryptionSecrets := make([]*corev1.Secret, 0, len(encryptionSecretList.Items))
	for _, secret := range encryptionSecretList.Items {
		if _, _, ok := secretToKeyAndMode(&secret, targetNamespace); !ok {
			klog.Infof("skipping invalid encryption secret %s", secret.Name)
			continue
		}
		encryptionSecrets = append(encryptionSecrets, secret.DeepCopy())
	}

	// make sure encryptionSecrets is sorted in DESCENDING order by keyID
	// this allows us to calculate the per resource write key as the first item and recent keys are first
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		// it is fine to ignore the validKeyID bool here because we filtered out invalid secrets in the loop above
		iKeyID, _ := secretToKeyID(encryptionSecrets[i])
		jKeyID, _ := secretToKeyID(encryptionSecrets[j])
		return iKeyID > jKeyID
	})

	// this is our output from the for loop below
	encryptionState := groupResourcesState{}

	// TODO if we cannot find the secrets associated with the keys, they may have been deleted
	// we could try to use the key data in the revision to recover or just go degraded permanently
	for gr, keys := range getGRsActualKeys(encryptionConfig) {
		writeSecret := findSecretForKey(keys.writeKey, encryptionSecrets, targetNamespace) // TODO handle nil as error when hasWriteKey == true?
		readSecrets := make([]*corev1.Secret, 0, len(keys.readKeys)+1)
		if keys.hasWriteKey() {
			readSecrets = append(readSecrets, writeSecret)
		}
		for _, readKey := range keys.readKeys {
			readSecret := findSecretForKey(readKey, encryptionSecrets, targetNamespace) // TODO handle nil as error?
			readSecrets = append(readSecrets, readSecret)
		}

		encryptionState[gr] = keysState{
			targetNamespace: targetNamespace,
			writeSecret:     writeSecret,
			readSecrets:     readSecrets,
		}
	}

	// see if any resource is missing and properly reflect that state so that
	// we will add the read keys and take it through the transition correctly.
	for gr := range encryptedGRs {
		if _, ok := encryptionState[gr]; !ok {
			encryptionState[gr] = keysState{
				targetNamespace: targetNamespace,
			}
		}
	}

	// TODO allow removing resources. This would require transitioning back to identity.  For now, because we work
	// against a single key, we can simply keep moving that additional resource even in the face of downgrades.

	allReadSecretsAsExpected := true
outer:
	for gr, grState := range encryptionState {
		if len(grState.readSecrets) != len(encryptionSecrets) {
			allReadSecretsAsExpected = false
			klog.V(4).Infof("%s has mismatching read secrets", gr)
			break
		}
		// order of read secrets does not matter here
		for _, encryptionSecret := range encryptionSecrets {
			if !hasSecret(grState.readSecrets, *encryptionSecret) {
				allReadSecretsAsExpected = false
				klog.V(4).Infof("%s missing read secret %s", gr, encryptionSecret.Name)
				break outer
			}
		}
	}
	// if our read secrets aren't all the same, the first thing to do is to force all the read secrets and wait for stability
	// TODO if the operand namespace gets deleted, this code causes us to go through a cycle of identity as the write key
	if !allReadSecretsAsExpected {
		klog.V(4).Infof("not all read secrets in sync")
		for gr := range encryptionState {
			grState := encryptionState[gr]
			grState.readSecrets = encryptionSecrets
			encryptionState[gr] = grState
		}
		return encryptionState
	}

	// we do not have any keys yet, so wait until we do
	// the code after this point assumes at least one secret
	if len(encryptionSecrets) == 0 {
		klog.V(4).Infof("no encryption secrets found")
		return encryptionState
	}

	// the first secret holds the write key
	writeSecret := encryptionSecrets[0]

	// we have consistent and completely current read secrets, the next step is determining a consistent write key.
	// To do this, we will choose the most current write key and use that to write the data
	allWriteSecretsAsExpected := true
	for gr, grState := range encryptionState {
		if grState.writeSecret == nil || grState.writeSecret.Name != writeSecret.Name {
			allWriteSecretsAsExpected = false
			klog.V(4).Infof("%s does not have write secret %s", gr, writeSecret.Name)
			break
		}
	}
	// if our write secrets aren't all the same, update all the write secrets and wait for stability.
	// We can move write keys before all the data has been migrated
	if !allWriteSecretsAsExpected {
		klog.V(4).Infof("not all write secrets in sync")
		for gr := range encryptionState {
			grState := encryptionState[gr]
			grState.writeSecret = writeSecret
			encryptionState[gr] = grState
		}
		return encryptionState
	}

	// at this point all of our read and write secrets are the same.  We need to inspect the write secret to
	// ensure that it has all the expect resources listed as having been migrated.  If this is true, then we
	// can prune the read secrets down to a single entry.

	migratedResourceString := writeSecret.Annotations[encryptionSecretMigratedResources]
	// if no migration has happened, we need to wait until it has
	if len(migratedResourceString) == 0 {
		klog.V(4).Infof("write secret %s has not been migrated", writeSecret.Name)
		return encryptionState
	}
	migratedResources := &migratedGroupResources{}
	// if we cannot read the data, wait until we can
	if err := json.Unmarshal([]byte(migratedResourceString), migratedResources); err != nil {
		klog.V(4).Infof("write secret %s has invalid migrated resource string: %v", writeSecret.Name, err)
		return encryptionState
	}

	// get a list of all the resources we have, so that we can compare against the migrated keys annotation.
	allResources := make([]schema.GroupResource, 0, len(encryptionState))
	for gr := range encryptionState {
		allResources = append(allResources, gr)
	}

	foundCount := 0
	for _, expected := range allResources {
		if migratedResources.hasResource(expected) {
			foundCount++
		}
	}
	// if we did not find migration indications for all resources, then just wait until we do
	if foundCount != len(allResources) {
		klog.V(4).Infof("write secret %s is missing migrated resources expected=%s, actual=%s", writeSecret.Name, allResources, migratedResources.Resources)
		return encryptionState
	}

	// if we have migrated all of our resources, the next step is remove all unnecessary read keys.
	// We only need the write key now
	for gr := range encryptionState {
		grState := encryptionState[gr]
		grState.readSecrets = []*corev1.Secret{writeSecret}
		encryptionState[gr] = grState
	}
	klog.V(4).Infof("write secret %s set as sole write key", writeSecret.Name)
	return encryptionState
}

type migratedGroupResources struct {
	Resources []schema.GroupResource `json:"resources"`
}

func (m *migratedGroupResources) hasResource(resource schema.GroupResource) bool {
	for _, gr := range m.Resources {
		if gr == resource {
			return true
		}
	}
	return false
}

func findSecretForKey(key keyAndMode, secrets []*corev1.Secret, targetNamespace string) *corev1.Secret {
	if key == (keyAndMode{}) {
		return nil
	}

	for _, secret := range secrets {
		sKeyAndMode, _, ok := secretToKeyAndMode(secret, targetNamespace)
		if !ok {
			continue
		}
		if sKeyAndMode == key {
			return secret.DeepCopy()
		}
	}

	return nil
}

func findSecretForKeyWithClient(key keyAndMode, secretClient corev1client.SecretsGetter, encryptionSecretSelector metav1.ListOptions, targetNamespace string) (*corev1.Secret, error) {
	if key == (keyAndMode{}) {
		return nil, nil
	}

	encryptionSecretList, err := secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(encryptionSecretSelector)
	if err != nil {
		return nil, err
	}

	for _, secret := range encryptionSecretList.Items {
		sKeyAndMode, _, ok := secretToKeyAndMode(&secret, targetNamespace)
		if !ok {
			continue
		}
		if sKeyAndMode == key {
			return secret.DeepCopy(), nil
		}
	}

	return nil, nil
}

func secretToKeyAndMode(encryptionSecret *corev1.Secret, targetNamespace string) (keyAndMode, uint64, bool) {
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
			Providers: secretsToProviders(grKeys),
		})
	}

	// make sure our output is stable
	sort.Slice(resourceConfigs, func(i, j int) bool {
		return resourceConfigs[i].Resources[0] < resourceConfigs[j].Resources[0] // each resource has its own keys
	})

	return resourceConfigs
}

func secretsToKeyAndModes(grKeys keysState) groupResourceKeys {
	desired := groupResourceKeys{}

	desired.writeKey = grKeys.writeKey()

	// keys have a duplicate of the write key
	// or there is no write key

	// we know these are sorted with highest key ID first
	readKeys := grKeys.readKeys()
	for i := range readKeys {
		readKey := readKeys[i]

		readKeyIsWriteKey := desired.hasWriteKey() && readKey == desired.writeKey
		// if present, do not include a duplicate write key in the read key list
		if !readKeyIsWriteKey {
			desired.readKeys = append(desired.readKeys, readKey)
		}

		// TODO consider being smarter about read keys we prune to avoid some rollouts
	}

	return desired
}

// secretsToProviders maps the write and read secrets to the equivalent read and write keys.
// it primarily handles the conversion of keyAndMode to the appropriate provider config.
// the identity mode is transformed into a custom aesgcm provider that simply exists to
// curry the associated null key secret through the encryption state machine.
func secretsToProviders(grKeys keysState) []apiserverconfigv1.ProviderConfiguration {
	desired := secretsToKeyAndModes(grKeys)

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
		// if encryption is not enabled at this revision or the secret was deleted, we should not error
		if errors.IsNotFound(err) {
			return &apiserverconfigv1.EncryptionConfiguration{}, nil
		}
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

// getGRsActualKeys parses the given encryptionConfig to determine the write and read keys per group resource.
// it assumes that the structure of the encryptionConfig matches the output generated by getResourceConfigs.
// each resource has a distinct configuration with zero or more key based providers and the identity provider.
// a special variant of the aesgcm provider is used to track the identity provider (since we need to keep the
// name of the key somewhere).  this is not an issue because aesgcm is not supported as a key provider since it
// is unsafe to use when you cannot control the number of writes (and we have no way to control apiserver writes).
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
	podClient corev1client.PodsGetter,
	secretClient corev1client.SecretsGetter,
	targetNamespace string,
	encryptionSecretSelector metav1.ListOptions,
	encryptedGRs map[schema.GroupResource]bool,
) (*apiserverconfigv1.EncryptionConfiguration, groupResourcesState, string, error) {
	revision, err := getAPIServerRevisionOfAllInstances(podClient.Pods(targetNamespace))
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

	encryptionSecretList, err := secretClient.Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).List(encryptionSecretSelector)
	if err != nil {
		return nil, nil, "", err
	}

	desiredEncryptionState := getDesiredEncryptionState(encryptionConfig, targetNamespace, encryptionSecretList, encryptedGRs)

	return encryptionConfig, desiredEncryptionState, "", nil
}

func hasSecret(secrets []*corev1.Secret, secret corev1.Secret) bool {
	for _, s := range secrets {
		if s.Name == secret.Name {
			return true
		}
	}
	return false
}
