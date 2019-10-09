package encryption

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/config/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

func getCurrentEncryptionConfig(secrets corev1client.SecretInterface, revision string) (*apiserverconfigv1.EncryptionConfiguration, error) {
	encryptionConfigSecret, err := secrets.Get(encryptionConfSecret+"-"+revision, metav1.GetOptions{})
	if err != nil {
		// if encryption is not enabled at this revision or the secret was deleted, we should not error
		if errors.IsNotFound(err) {
			return &apiserverconfigv1.EncryptionConfiguration{}, nil
		}
		return nil, err
	}

	decoder := apiserverCodecs.UniversalDecoder(apiserverconfigv1.SchemeGroupVersion)
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

// getDesiredEncryptionState returns the desired state of encryption for all resources.
// To do this it compares the current state against the available secrets and to-be-encrypted resources.
// oldEncryptionConfig can be nil if there is no config yet.
//
// The basic rules are:
//
// 1. don't do anything if there are key secrets.
// 2. every GR must have all the read-keys (existing as secrets) since last complete migration.
// 3. if (2) is the case, the write-key must be the most recent key.
// 4. if (2) and (3) are the case, all non-write keys should be removed.
func getDesiredEncryptionState(oldEncryptionConfig *apiserverconfigv1.EncryptionConfiguration, targetNamespace string, encryptionSecrets []*corev1.Secret, toBeEncryptedGRs []schema.GroupResource) groupResourcesState {
	encryptionSecrets = sortRecentFirst(encryptionSecrets)

	//
	// STEP 0: start with old encryption config, and alter is in the rest of the rest of the func towards the desired state.
	//
	desiredEncryptionState := encryptionConfigToEncryptionState(targetNamespace, oldEncryptionConfig)

	//
	// STEP 1: without secrets, wait for the key controller to create one
	//
	// the code after this point assumes at least one secret
	if len(encryptionSecrets) == 0 {
		klog.V(4).Infof("no encryption secrets found")
		return desiredEncryptionState
	}
	if desiredEncryptionState == nil {
		desiredEncryptionState = groupResourcesState{}
	}

	// add new resources without keys. These resources will trigger STEP 2.
	for _, gr := range toBeEncryptedGRs {
		if _, ok := desiredEncryptionState[gr]; !ok {
			desiredEncryptionState[gr] = keysState{
				targetNamespace: targetNamespace,
			}
		}
	}

	//
	// STEP 2: verify to have all necessary read-keys. If not, add them, deploy and wait for stability.
	//
	// Note: we never drop keys here. Dropping only happens in STEP 4.
	// Note: only keysWithPotentiallyPersistedData are considered. There might be more which are not pruned yet by the pruning controller.
	//
	// TODO: allow removing resources (e.g. on downgrades) and transition back to identity.
	allReadSecretsAsExpected := true
	currentlyEncryptedGRs := make([]schema.GroupResource, 0, len(desiredEncryptionState))
	if oldEncryptionConfig != nil {
		for gr := range getGRsActualKeys(oldEncryptionConfig) {
			currentlyEncryptedGRs = append(currentlyEncryptedGRs, gr)
		}
	} else {
		// if the config is not there, we assume it was deleted. Assume all toBeEncryptedGRs were
		// encrypted before and key matching secret keys.
		currentlyEncryptedGRs = toBeEncryptedGRs
	}
	expectedReadSecrets := keysWithPotentiallyPersistedData(currentlyEncryptedGRs, encryptionSecrets)
	for gr, grState := range desiredEncryptionState {
		changed := false
		for _, expected := range expectedReadSecrets {
			if !hasSecret(grState.readSecrets, expected) {
				grState.readSecrets = append(grState.readSecrets, expected)
				changed = true
				allReadSecretsAsExpected = false
				klog.V(4).Infof("%s missing read secret %s", gr, expected.Name)
			}
		}
		if changed {
			desiredEncryptionState[gr] = grState
		}
	}
	if !allReadSecretsAsExpected {
		klog.V(4).Infof("not all read secrets in sync")
		return desiredEncryptionState
	}

	//
	// STEP 3: with consistent read-keys, verify first read-key is write-key. If not, set write-key and wait for stability.
	//
	writeSecret := encryptionSecrets[0]
	allWriteSecretsAsExpected := true
	for gr, grState := range desiredEncryptionState {
		if grState.writeSecret == nil || grState.writeSecret.Name != writeSecret.Name {
			allWriteSecretsAsExpected = false
			klog.V(4).Infof("%s does not have write secret %s", gr, writeSecret.Name)
			break
		}
	}
	if !allWriteSecretsAsExpected {
		klog.V(4).Infof("not all write secrets in sync")
		for gr := range desiredEncryptionState {
			grState := desiredEncryptionState[gr]
			grState.writeSecret = writeSecret
			desiredEncryptionState[gr] = grState
		}
		return desiredEncryptionState
	}

	//
	// STEP 4: with consistent read-keys and write-keys, remove every read-key other than the write-key.
	//
	// Note: because read-keys are consistent, currentlyEncryptedGRs equals toBeEncryptedGRs
	allMigrated, _, reason := migratedFor(currentlyEncryptedGRs, writeSecret)
	if !allMigrated {
		klog.V(4).Infof(reason)
		return desiredEncryptionState
	}
	for gr := range desiredEncryptionState {
		grState := desiredEncryptionState[gr]
		grState.readSecrets = []*corev1.Secret{writeSecret}
		desiredEncryptionState[gr] = grState
	}
	klog.V(4).Infof("write secret %s set as sole write key", writeSecret.Name)
	return desiredEncryptionState
}

// migratedFor returns whether all given resources are marked as migrated in the given secret.
// It returns missing GRs and a reason if that's not the case.
func migratedFor(grs []schema.GroupResource, s *corev1.Secret) (ok bool, missing []schema.GroupResource, reason string) {
	migratedResourceString := s.Annotations[encryptionSecretMigratedResources]
	if len(migratedResourceString) == 0 {
		return false, grs, fmt.Sprintf("secret %s has not been migrated", s.Name)
	}

	migrated := &migratedGroupResources{}
	if err := json.Unmarshal([]byte(migratedResourceString), migrated); err != nil {
		return false, grs, fmt.Sprintf("secret %s has invalid migrated resource string: %v", s.Name, err)
	}

	var missingStrings []string
	for _, gr := range grs {
		if !migrated.hasResource(gr) {
			missing = append(missing, gr)
			missingStrings = append(missingStrings, gr.String())
		}
	}

	if len(missing) > 0 {
		return false, missing, fmt.Sprintf("secret(s) %s misses resource %s among migrated resources", s.Name, strings.Join(missingStrings, ","))
	}

	return true, nil, ""
}

// keysWithPotentiallyPersistedData returns the minimal, recent secrets which have migrated all given GRs.
func keysWithPotentiallyPersistedData(grs []schema.GroupResource, recentFirstSortedSecrets []*corev1.Secret) []*corev1.Secret {
	for i, s := range recentFirstSortedSecrets {
		if allMigrated, missing, _ := migratedFor(grs, s); allMigrated {
			return recentFirstSortedSecrets[:i+1]
		} else {
			// continue with keys we haven't found a migration key for yet
			grs = missing
		}
	}
	return recentFirstSortedSecrets
}

func encryptionConfigToEncryptionState(targetNamespace string, c *apiserverconfigv1.EncryptionConfiguration) groupResourcesState {
	if c == nil {
		return nil
	}

	// convert old config to the base of desired state
	s := groupResourcesState{}
	for gr, keys := range getGRsActualKeys(c) {
		readSecrets := make([]*corev1.Secret, 0, len(keys.readKeys)+1)

		var writeSecret *corev1.Secret
		if keys.hasWriteKey() {
			var err error
			writeSecret, err = keyAndModeToKeySecret(targetNamespace, keys.writeKey)
			if err != nil {
				klog.Warningf("skipping invalid write-key from encryption config for resource %s: %v", gr, err)
			} else {
				readSecrets = append(readSecrets, writeSecret)
			}
		}

		for _, readKey := range keys.readKeys {
			readSecret, err := keyAndModeToKeySecret(targetNamespace, readKey)
			if err != nil {
				klog.Warningf("skipping invalid read-key from encryption config for resource %s: %v", gr, err)
			} else {
				readSecrets = append(readSecrets, readSecret)
			}
		}

		s[gr] = keysState{
			targetNamespace: targetNamespace,
			writeSecret:     writeSecret,
			readSecrets:     readSecrets,
		}
	}
	return s
}

func keyAndModeToKeySecret(targetNamespace string, k keyAndMode) (*corev1.Secret, error) {
	bs, err := base64.StdEncoding.DecodeString(k.key.Secret)
	if err != nil {
		return nil, fmt.Errorf("failed decoding base64 data of key %s", k.key.Name)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-encryption-%s", targetNamespace, k.key.Name),
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Labels: map[string]string{
				encryptionSecretComponent: targetNamespace,
			},
			Annotations: map[string]string{
				encryptionSecretMode: string(k.mode),
			},
		},
		Data: map[string][]byte{
			encryptionSecretKeyData: bs,
		},
	}, nil
}

func sortRecentFirst(encryptionSecrets []*corev1.Secret) []*corev1.Secret {
	ret := make([]*corev1.Secret, len(encryptionSecrets))
	copy(ret, encryptionSecrets)
	sort.Slice(ret, func(i, j int) bool {
		// it is fine to ignore the validKeyID bool here because we filtered out invalid secrets in the loop above
		iKeyID, _ := secretToKeyID(ret[i])
		jKeyID, _ := secretToKeyID(ret[j])
		return iKeyID > jKeyID
	})
	return ret
}

// getGRsActualKeys parses the given encryptionConfig to determine the write and read keys per group resource.
// it assumes that the structure of the encryptionConfig matches the output generated by getResourceConfigs.
// each resource has a distinct configuration with zero or more key based providers and the identity provider.
// a special variant of the aesgcm provider is used to track the identity provider (since we need to keep the
// name of the key somewhere).  this is not an issue because aesgcm is not supported as a key provider since it
// is unsafe to use when you cannot control the number of writes (and we have no way to control apiserver writes).
func getGRsActualKeys(encryptionConfig *apiserverconfigv1.EncryptionConfiguration) map[schema.GroupResource]groupResourceKeys {
	if encryptionConfig == nil {
		return nil
	}

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
	var encryptionSecrets []*corev1.Secret
	for i, s := range encryptionSecretList.Items {
		if _, _, ok := secretToKeyAndMode(&s, targetNamespace); !ok {
			klog.Infof("skipping invalid encryption secret %s", s.Name)
			continue
		} else {
			encryptionSecrets = append(encryptionSecrets, &encryptionSecretList.Items[i])
		}
	}

	var encryptedGRsList []schema.GroupResource
	for gr := range encryptedGRs {
		encryptedGRsList = append(encryptedGRsList, gr)
	}

	desiredEncryptionState := getDesiredEncryptionState(encryptionConfig, targetNamespace, encryptionSecrets, encryptedGRsList)

	return encryptionConfig, desiredEncryptionState, "", nil
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

func hasSecret(secrets []*corev1.Secret, secret *corev1.Secret) bool {
	for _, s := range secrets {
		if s.Name == secret.Name {
			return true
		}
	}
	return false
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
