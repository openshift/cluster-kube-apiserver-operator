package encryption

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library"
	operatorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

var (
	waitPollInterval = 15 * time.Second
	waitPollTimeout  = 60 * time.Minute // a happy path scenario needs to roll out 3 revisions each taking ~10 min
)

type ClientSet struct {
	Etcd            EtcdClient
	ApiServerConfig configv1client.APIServerInterface
	Operator        operatorv1client.KubeAPIServerInterface
	Kube            kubernetes.Interface
}

func TestEncryptionTypeAESCBC(t *testing.T) {
	e := NewE(t)
	clientSet := SetAndWaitForEncryptionType(e, configv1.EncryptionTypeAESCBC)
	AssertSecretsAndConfigMaps(e, clientSet, string(configv1.EncryptionTypeAESCBC))
}

func SetAndWaitForEncryptionType(t testing.TB, encryptionType configv1.EncryptionType) ClientSet {
	t.Helper()
	t.Logf("Starting encryption e2e test for %q mode", encryptionType)

	clientSet := GetClients(t)

	apiServer, err := clientSet.ApiServerConfig.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	needsUpdate := apiServer.Spec.Encryption.Type != encryptionType
	if needsUpdate {
		t.Logf("Updating encryption type in the config file for APIServer to %q", encryptionType)
		apiServer.Spec.Encryption.Type = encryptionType
		_, err = clientSet.ApiServerConfig.Update(apiServer)
		require.NoError(t, err)
	} else {
		t.Logf("APIServer is already configured to use %q mode", encryptionType)
	}

	WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded(t, clientSet.Operator)
	return clientSet
}

func GetClients(t testing.TB) ClientSet {
	t.Helper()

	kubeConfig, err := library.NewClientConfigForTest()
	require.NoError(t, err)

	configClient := configv1client.NewForConfigOrDie(kubeConfig)
	apiServerConfigClient := configClient.APIServers()

	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)
	etcdClient := NewEtcdClient(kubeClient)

	operatorClient, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return ClientSet{Etcd: etcdClient, ApiServerConfig: apiServerConfigClient, Operator: operatorClient.KubeAPIServers(), Kube: kubeClient}
}

// WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded waits for the operator and encryption migration controller to report status as active not progressing, and not failing
func WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded(t testing.TB, operatorClient operatorv1client.KubeAPIServerInterface) {
	t.Helper()
	t.Log("Waiting for Operator and Migration Controller to be: OperatorAvailable: true, OperatorProgressing: false, MigrationProgressing: false, MigrationDegraded: false, Done: true (Migration and Operator not progressing for > 1 min)")
	// given that a happy path scenario needs to roll out at least 3 revision (each taking ~10 min)
	// the loop below will need at least 30 min to be satisfied.
	// we sleep here 5 minutes before we enter the loop to make sure that the condition
	// will not be satisfied immediately
	// TODO: consider watching other conditions and events - things like that indicate we make progress
	time.Sleep(5 * time.Minute)

	observedOperatorStateAsString := ""
	if err := wait.Poll(waitPollInterval, waitPollTimeout, func() (bool, error) {
		clusterOperator, err := operatorClient.Get("cluster", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			t.Log("KubeAPIServer/cluster operator does not yet exist.")
			return false, nil
		}
		if err != nil {
			t.Log("Unable to retrieve KubeAPIServer/cluster operator:", err)
			return false, nil
		}

		conditions := clusterOperator.Status.Conditions
		operatorAvailable := operatorhelpers.IsOperatorConditionPresentAndEqual(conditions, operatorv1.OperatorStatusTypeAvailable, operatorv1.ConditionTrue)
		operatorNotProgressing := operatorhelpers.IsOperatorConditionPresentAndEqual(conditions, operatorv1.OperatorStatusTypeProgressing, operatorv1.ConditionFalse)
		migrationNotProgressing := operatorhelpers.IsOperatorConditionFalse(conditions, "EncryptionMigrationControllerProgressing")
		migrationNotDegraded := operatorhelpers.IsOperatorConditionFalse(conditions, "EncryptionMigrationControllerDegraded")

		// note that migration needs to roll out more than one revision,
		// not having the operator and the migration controller progressing for at least one minute
		// seems to be a good indicator that migration has finished
		done := operatorAvailable && operatorNotProgressing && migrationNotProgressing && migrationNotDegraded
		done = done && time.Since(operatorhelpers.FindOperatorCondition(conditions, operatorv1.OperatorStatusTypeProgressing).LastTransitionTime.Time) > time.Minute
		done = done && time.Since(operatorhelpers.FindOperatorCondition(conditions, "EncryptionMigrationControllerProgressing").LastTransitionTime.Time) > time.Minute
		currentOperatorStateAsString := fmt.Sprintf("Operator and Migration Controller is: OperatorAvailable: %v, OperatorProgressing: %v, MigrationProgressing: %v  MigrationDegraded: %v  Done: %v", operatorAvailable, !operatorNotProgressing, !migrationNotProgressing, !migrationNotDegraded, done)
		if currentOperatorStateAsString != observedOperatorStateAsString {
			t.Log(currentOperatorStateAsString)
			observedOperatorStateAsString = currentOperatorStateAsString
		}

		return done, nil
	}); err != nil {
		t.Fatalf("Failed waiting for Operator and Migration Controller due to %v", err)
	}
}

func WaitForNextMigratedKey(t testing.TB, kubeClient kubernetes.Interface, prevKeyName string, prevKeyMigratedRes []schema.GroupResource) {
	t.Helper()

	nextKeyName := ""
	if len(prevKeyName) > 0 {
		prevKeyID, prevKeyValid := nameToKeyID(prevKeyName)
		if !prevKeyValid {
			t.Errorf("Invalid key %q passed", prevKeyName)
		}
		nexKeyID := prevKeyID + 1
		nextKeyName = strings.Replace(prevKeyName, fmt.Sprintf("%d", prevKeyID), fmt.Sprintf("%d", nexKeyID), 1)
	} else {
		prevKeyName = "no previous key"
		nextKeyName = "encryption-key-openshift-kube-apiserver-1"
		prevKeyMigratedRes = defaultTargetGRs
	}
	t.Logf("Waiting for the next key %q, previous key was %q", nextKeyName, prevKeyName)

	observedKeyName := prevKeyName
	if err := wait.Poll(waitPollInterval, waitPollTimeout, func() (bool, error) {
		currentKeyName, migratedResourcesForCurrentKey, err := GetLastKeyMeta(kubeClient)
		if err != nil {
			return false, err
		}

		if currentKeyName != observedKeyName {
			if currentKeyName != nextKeyName {
				return false, fmt.Errorf("unexpected key observed %q, expected %q", currentKeyName, nextKeyName)
			}
			t.Logf("Observed key %q, waiting until it will be used to migrate %v", currentKeyName, prevKeyMigratedRes)
			observedKeyName = currentKeyName
		}

		if currentKeyName == nextKeyName {
			if len(prevKeyMigratedRes) == len(migratedResourcesForCurrentKey) {
				for _, expectedGR := range prevKeyMigratedRes {
					if !hasResource(expectedGR, prevKeyMigratedRes) {
						return false, nil
					}
				}
				t.Logf("Key %q was used to migrate %v", currentKeyName, migratedResourcesForCurrentKey)
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("Failed waiting for key %s to be used to migrate %v, due to %v", nextKeyName, prevKeyMigratedRes, err)
	}
}

func GetLastKeyMeta(kubeClient kubernetes.Interface) (string, []schema.GroupResource, error) {
	secretsClient := kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)
	selectedSecrets, err := secretsClient.List(metav1.ListOptions{LabelSelector: "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace})
	if err != nil {
		return "", nil, err
	}

	if len(selectedSecrets.Items) == 0 {
		return "", nil, nil
	}
	encryptionSecrets := make([]*corev1.Secret, 0, len(selectedSecrets.Items))
	for _, s := range selectedSecrets.Items {
		encryptionSecrets = append(encryptionSecrets, s.DeepCopy())
	}
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		iKeyID, _ := nameToKeyID(encryptionSecrets[i].Name)
		jKeyID, _ := nameToKeyID(encryptionSecrets[j].Name)
		return iKeyID > jKeyID
	})
	lastKey := encryptionSecrets[0]

	type migratedGroupResources struct {
		Resources []schema.GroupResource `json:"resources"`
	}

	migrated := &migratedGroupResources{}
	if v, ok := lastKey.Annotations["encryption.apiserver.operator.openshift.io/migrated-resources"]; ok && len(v) > 0 {
		if err := json.Unmarshal([]byte(v), migrated); err != nil {
			return "", nil, err
		}
	}
	return lastKey.Name, migrated.Resources, nil
}

func ForceKeyRotation(t testing.TB, operatorClient operatorv1client.KubeAPIServerInterface, reason string) error {
	t.Logf("Forcing a new key rotation, reason %q", reason)
	data := map[string]map[string]string{
		"encryption": {
			"reason": reason,
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		apiServerOperator, err := operatorClient.Get("cluster", metav1.GetOptions{})
		if err != nil {
			return err
		}
		apiServerOperator.Spec.UnsupportedConfigOverrides.Raw = raw
		_, err = operatorClient.Update(apiServerOperator)
		return err
	})
}

// hasResource returns whether the given group resource is contained in the migrated group resource list.
func hasResource(expectedResource schema.GroupResource, actualResources []schema.GroupResource) bool {
	for _, gr := range actualResources {
		if gr == expectedResource {
			return true
		}
	}
	return false
}

func nameToKeyID(name string) (uint64, bool) {
	lastIdx := strings.LastIndex(name, "-")
	idString := name
	if lastIdx >= 0 {
		idString = name[lastIdx+1:] // this can never overflow since str[-1+1:] is
	}
	id, err := strconv.ParseUint(idString, 10, 0)
	return id, err == nil
}
