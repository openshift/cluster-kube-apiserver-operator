package encryption

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/test/library"
)

var (
	waitPollInterval      = 15 * time.Second
	waitPollTimeout       = 60 * time.Minute // a happy path scenario needs to roll out 3 revisions each taking ~10 min
	defaultEncryptionMode = string(configv1.EncryptionTypeIdentity)
)

type ClientSet struct {
	Etcd            EtcdClient
	ApiServerConfig configv1client.APIServerInterface
	Operator        operatorv1client.KubeAPIServerInterface
	Kube            kubernetes.Interface
}

type EncryptionKeyMeta struct {
	Name     string
	Migrated []schema.GroupResource
	Mode     string
}

func SetUp(t *testing.T, verboseTearDown ...bool) testing.TB {
	e := newE(t)

	verboseOutuptOnTearDown := true
	if len(verboseTearDown) > 0 {
		verboseOutuptOnTearDown = verboseTearDown[0]
	}
	e.registerTearDownFun(setUpTearDown(verboseOutuptOnTearDown))
	return e
}

func TestEncryptionTypeAESCBC(t *testing.T) {
	e := SetUp(t)
	clientSet := SetAndWaitForEncryptionType(e, configv1.EncryptionTypeAESCBC)
	AssertSecretsAndConfigMaps(e, clientSet, configv1.EncryptionTypeAESCBC)
}

func SetAndWaitForEncryptionType(t testing.TB, encryptionType configv1.EncryptionType) ClientSet {
	t.Helper()
	t.Logf("Starting encryption e2e test for %q mode", encryptionType)

	clientSet := GetClients(t)
	lastMigratedKeyMeta, err := GetLastKeyMeta(clientSet.Kube)
	require.NoError(t, err)

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

	WaitForEncryptionKeyBasedOn(t, clientSet.Kube, lastMigratedKeyMeta, encryptionType)
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

func WaitForEncryptionKeyBasedOn(t testing.TB, kubeClient kubernetes.Interface, prevKeyMeta EncryptionKeyMeta, encryptionType configv1.EncryptionType) {
	encryptionMode := string(encryptionType)
	if encryptionMode == "" {
		encryptionMode = defaultEncryptionMode
	}
	if len(prevKeyMeta.Name) == 0 {
		prevKeyMeta.Mode = defaultEncryptionMode
	}

	if prevKeyMeta.Mode == encryptionMode {
		waitForNoNewEncryptionKey(t, kubeClient, prevKeyMeta)
		return
	}
	WaitForNextMigratedKey(t, kubeClient, prevKeyMeta)
}

func waitForNoNewEncryptionKey(t testing.TB, kubeClient kubernetes.Interface, prevKeyMeta EncryptionKeyMeta) {
	t.Helper()
	// given that the happy path scenario needs ~30 min
	// waiting 5 min to see if a new key hasn't been created seems to be enough.
	waitNoKeyPollInterval := 15 * time.Second
	waitNoKeyPollTimeout := 6 * time.Minute
	waitDuration := 5 * time.Minute

	nextKeyName, err := determineNextEncryptionKeyName(prevKeyMeta.Name)
	require.NoError(t, err)
	t.Logf("Waiting up to %s to check if no new key %q will be crated, as the previous (%q) key's encryption mode (%q) is the same as the current/desired one", waitDuration.String(), nextKeyName, prevKeyMeta.Name, prevKeyMeta.Mode)

	observedTimestamp := time.Now()
	if err := wait.Poll(waitNoKeyPollInterval, waitNoKeyPollTimeout, func() (bool, error) {
		currentKeyMeta, err := GetLastKeyMeta(kubeClient)
		if err != nil {
			return false, err
		}

		if currentKeyMeta.Name != prevKeyMeta.Name {
			return false, fmt.Errorf("unexpected key observed %q, expected no new key", currentKeyMeta.Name)
		}

		if time.Since(observedTimestamp) > waitDuration {
			t.Logf("Haven't seen a new key for %s", waitDuration.String())
			return true, nil
		}

		return false, nil
	}); err != nil {
		newErr := fmt.Errorf("failed to check if no new key will be created, err %v", err)
		require.NoError(t, newErr)
	}
}

func WaitForNextMigratedKey(t testing.TB, kubeClient kubernetes.Interface, prevKeyMeta EncryptionKeyMeta) {
	t.Helper()

	var err error
	nextKeyName := ""
	nextKeyName, err = determineNextEncryptionKeyName(prevKeyMeta.Name)
	require.NoError(t, err)
	if len(prevKeyMeta.Name) == 0 {
		prevKeyMeta.Name = "no previous key"
		prevKeyMeta.Migrated = defaultTargetGRs
	}

	t.Logf("Waiting up to %s for the next key %q, previous key was %q", waitPollTimeout.String(), nextKeyName, prevKeyMeta.Name)
	observedKeyName := prevKeyMeta.Name
	if err := wait.Poll(waitPollInterval, waitPollTimeout, func() (bool, error) {
		currentKeyMeta, err := GetLastKeyMeta(kubeClient)
		if err != nil {
			return false, err
		}

		if currentKeyMeta.Name != observedKeyName {
			if currentKeyMeta.Name != nextKeyName {
				return false, fmt.Errorf("unexpected key observed %q, expected %q", currentKeyMeta.Name, nextKeyName)
			}
			t.Logf("Observed key %q, waiting up to %s until it will be used to migrate %v", currentKeyMeta.Name, waitPollTimeout.String(), prevKeyMeta.Migrated)
			observedKeyName = currentKeyMeta.Name
		}

		if currentKeyMeta.Name == nextKeyName {
			if len(prevKeyMeta.Migrated) == len(currentKeyMeta.Migrated) {
				for _, expectedGR := range prevKeyMeta.Migrated {
					if !hasResource(expectedGR, prevKeyMeta.Migrated) {
						return false, nil
					}
				}
				t.Logf("Key %q was used to migrate %v", currentKeyMeta.Name, currentKeyMeta.Migrated)
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		newErr := fmt.Errorf("failed waiting for key %s to be used to migrate %v, due to %v", nextKeyName, prevKeyMeta.Migrated, err)
		require.NoError(t, newErr)
	}
}

func GetLastKeyMeta(kubeClient kubernetes.Interface) (EncryptionKeyMeta, error) {
	secretsClient := kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)
	selectedSecrets, err := secretsClient.List(metav1.ListOptions{LabelSelector: "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace})
	if err != nil {
		return EncryptionKeyMeta{}, err
	}

	if len(selectedSecrets.Items) == 0 {
		return EncryptionKeyMeta{}, nil
	}
	encryptionSecrets := make([]*corev1.Secret, 0, len(selectedSecrets.Items))
	for _, s := range selectedSecrets.Items {
		encryptionSecrets = append(encryptionSecrets, s.DeepCopy())
	}
	sort.Slice(encryptionSecrets, func(i, j int) bool {
		iKeyID, _ := encryptionKeyNameToKeyID(encryptionSecrets[i].Name)
		jKeyID, _ := encryptionKeyNameToKeyID(encryptionSecrets[j].Name)
		return iKeyID > jKeyID
	})
	lastKey := encryptionSecrets[0]

	type migratedGroupResources struct {
		Resources []schema.GroupResource `json:"resources"`
	}

	migrated := &migratedGroupResources{}
	if v, ok := lastKey.Annotations["encryption.apiserver.operator.openshift.io/migrated-resources"]; ok && len(v) > 0 {
		if err := json.Unmarshal([]byte(v), migrated); err != nil {
			return EncryptionKeyMeta{}, err
		}
	}
	mode := lastKey.Annotations["encryption.apiserver.operator.openshift.io/mode"]

	return EncryptionKeyMeta{Name: lastKey.Name, Migrated: migrated.Resources, Mode: mode}, nil
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

func CreateAndStoreSecretOfLife(t testing.TB, clientSet ClientSet) *corev1.Secret {
	t.Helper()
	{
		oldSecretOfLife, err := clientSet.Kube.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Get("secret-of-life", metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			t.Errorf("Failed to check if the secret already exists, due to %v", err)
		}
		if len(oldSecretOfLife.Name) > 0 {
			t.Log("The secret already exist, removing it first")
			err := clientSet.Kube.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Delete(oldSecretOfLife.Name, &metav1.DeleteOptions{})
			if err != nil {
				t.Errorf("Failed to delete %s, err %v", oldSecretOfLife.Name, err)
			}
		}
	}
	t.Logf("Creating %q in %s namespace", "secret-of-life", operatorclient.GlobalMachineSpecifiedConfigNamespace)
	secretOfLife, err := clientSet.Kube.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace).Create(SecretOfLife(t))
	require.NoError(t, err)
	return secretOfLife
}

func SecretOfLife(t testing.TB) *corev1.Secret {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "secret-of-life",
		},
		Data: map[string][]byte{
			"quote": []byte("I have no special talents. I am only passionately curious"),
		},
	}
}

func setUpTearDown(verboseOutput bool) func(testing.TB, bool) {
	return func(t testing.TB, failed bool) {
		if failed && verboseOutput { // we don't use t.Failed() because we handle termination differently when running on a local machine
			t.Logf("Tearing Down %s", t.Name())
			clientSet := GetClients(t)

			eventList, err := clientSet.Kube.CoreV1().Events(operatorclient.OperatorNamespace).List(metav1.ListOptions{})
			require.NoError(t, err)

			sort.Slice(eventList.Items, func(i, j int) bool {
				first := eventList.Items[i]
				second := eventList.Items[j]
				return first.LastTimestamp.After(second.LastTimestamp.Time)
			})

			t.Logf("Events from %q namespace", operatorclient.OperatorNamespace)
			now := time.Now()
			for _, ev := range eventList.Items {
				t.Logf("Last seen: %-15v Type: %-10v Reason: %-40v Source: %-55v Message: %v", now.Sub(ev.LastTimestamp.Time), ev.Type, ev.Reason, ev.Source.Component, ev.Message)
			}
		}
	}
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

func encryptionKeyNameToKeyID(name string) (uint64, bool) {
	lastIdx := strings.LastIndex(name, "-")
	idString := name
	if lastIdx >= 0 {
		idString = name[lastIdx+1:] // this can never overflow since str[-1+1:] is
	}
	id, err := strconv.ParseUint(idString, 10, 0)
	return id, err == nil
}

func determineNextEncryptionKeyName(prevKeyName string) (string, error) {
	if len(prevKeyName) > 0 {
		prevKeyID, prevKeyValid := encryptionKeyNameToKeyID(prevKeyName)
		if !prevKeyValid {
			return "", fmt.Errorf("invalid key %q passed", prevKeyName)
		}
		nexKeyID := prevKeyID + 1
		return strings.Replace(prevKeyName, fmt.Sprintf("%d", prevKeyID), fmt.Sprintf("%d", nexKeyID), 1), nil
	}

	// no encryption key - the first one will look like the following
	return "encryption-key-openshift-kube-apiserver-1", nil
}

func GetRawSecretOfLife(t testing.TB, clientSet ClientSet) string {
	t.Helper()
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	secretOfLifeEtcdPrefix := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", operatorclient.GlobalMachineSpecifiedConfigNamespace, "secret-of-life")
	resp, err := clientSet.Etcd.Get(timeout, secretOfLifeEtcdPrefix, clientv3.WithPrefix())
	require.NoError(t, err)

	if len(resp.Kvs) != 1 {
		t.Errorf("Expected to get a single key from etcd, got %d", len(resp.Kvs))
	}

	return string(resp.Kvs[0].Value)
}
