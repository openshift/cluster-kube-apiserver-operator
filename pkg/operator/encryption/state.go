package encryption

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

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

func hasSecret(secrets []*corev1.Secret, secret corev1.Secret) bool {
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
