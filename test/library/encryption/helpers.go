package encryption

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library"
	operatorhelpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

var (
	waitPollInterval = 15 * time.Second
	waitPollTimeout  = 60 * time.Minute // a happy path scenario needs to roll out 3 revisions each taking ~10 min
)

func TestEncryptionTypeAESCBC(t *testing.T) {
	e := NewE(t)
	etcdClient := TestEncryptionType(e, configv1.EncryptionTypeAESCBC)
	AssertSecretsAndConfigMaps(e, etcdClient, string(configv1.EncryptionTypeAESCBC))
}

func TestEncryptionType(t testing.TB, encryptionType configv1.EncryptionType) EtcdClient {
	t.Helper()
	t.Logf("Starting encryption e2e test for %q mode", encryptionType)

	etcdClient, apiServerClient, operatorClient := GetClients(t)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	needsUpdate := apiServer.Spec.Encryption.Type != encryptionType
	if needsUpdate {
		t.Logf("Updating encryption type in the config file for APIServer to %q", encryptionType)
		apiServer.Spec.Encryption.Type = encryptionType
		_, err = apiServerClient.Update(apiServer)
		require.NoError(t, err)
	} else {
		t.Logf("APIServer is already configured to use %q mode", encryptionType)
	}

	WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded(t, operatorClient)
	return etcdClient
}

func GetClients(t testing.TB) (EtcdClient, configv1client.APIServerInterface, operatorclient.KubeAPIServerInterface) {
	t.Helper()

	kubeConfig, err := library.NewClientConfigForTest()
	require.NoError(t, err)

	configClient := configv1client.NewForConfigOrDie(kubeConfig)
	apiServerConfigClient := configClient.APIServers()

	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)
	etcdClient := NewEtcdClient(kubeClient)

	operatorClient, err := operatorclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	return etcdClient, apiServerConfigClient, operatorClient.KubeAPIServers()
}

// WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded waits for the operator and encryption migration controller to report status as active not progressing, and not failing
func WaitForOperatorAndMigrationControllerAvailableNotProgressingNotDegraded(t testing.TB, operatorClient operatorclient.KubeAPIServerInterface) {
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
