package encryption

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
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

	// described whether it is backed by a secret.
	backed   bool
	migrated Migration
	// some controller logic caused this secret to be created by the key controller.
	internalReason string
	// the user via unsupportConfigOverrides.encryption.reason triggered this key.
	externalReason string
}

type Migration struct {
	// the timestamp fo the last migration
	ts time.Time
	// the resources that were migrated at some point in time to this key.
	resources []schema.GroupResource
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

// migratedGroupResources is the data structured stored in the
// encryption.apiserver.operator.openshift.io/migrated-resources
// of a key secret.
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
