package e2e_encryption

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestEncryptionTypeIdentity(t *testing.T) {
	kv, done := testEncryptionType(t, configv1.EncryptionTypeIdentity)
	defer done()

	test.CheckEtcdSecretsAndConfigMapsMust(t, kv, test.CheckEncryptionState("identity-proto"))
}

func TestEncryptionTypeAESCBC(t *testing.T) {
	kv, done := testEncryptionType(t, configv1.EncryptionTypeAESCBC)
	defer done()

	test.CheckEtcdSecretsAndConfigMapsMust(t, kv, test.CheckEncryptionState("aescbc"))
}

func TestEncryptionRotation(t *testing.T) {
	t.Skip()
	kv, done, _, apiServerClient, kubeClient, operatorClient := getEncryptionClients(t)
	defer done()
	secretsClient := kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	apiServer.Spec.Encryption.Type = configv1.EncryptionTypeAESCBC
	_, err = apiServerClient.Update(apiServer)
	require.NoError(t, err)

	// make sure any changes we made have been observed
	time.Sleep(time.Minute)

	keyPrefixes := make([]string, 3)

	// run a few rotations and assert that migrations occur as expected and keyIDs increase
	for i := range keyPrefixes {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			keyPrefixes[i] = testRotation(t, operatorClient, secretsClient, kv)
		})
		if t.Failed() {
			return
		}
	}

	require.Truef(t, sort.IsSorted(sort.StringSlice(keyPrefixes)), "key IDs not in ascending order: %v", keyPrefixes)
}

func TestEncryptionTypeUnset(t *testing.T) {
	kv, done := testEncryptionType(t, "")
	defer done()

	test.CheckEtcdSecretsAndConfigMapsMust(t, kv, test.CheckEncryptionState("identity-proto"))
}

func TestEncryptionTurnOnAndOff(t *testing.T) {
	for i, f := range []func(*testing.T){
		TestEncryptionTypeAESCBC,
		TestEncryptionTypeIdentity,
		TestEncryptionTypeAESCBC,
		TestEncryptionTypeIdentity,
	} {
		now := time.Now()
		fmt.Printf("LCL %s\n", now.Format(time.RFC3339))
		fmt.Printf("UTC %s\n", now.UTC().Format(time.RFC3339))
		t.Run(strconv.Itoa(i), f)
		if t.Failed() {
			return
		}
	}
}

func testEncryptionType(t *testing.T, encryptionType configv1.EncryptionType) (test.EtcdGetter, func()) {
	t.Helper()

	kv, done, configClient, apiServerClient, kubeClient, _ := getEncryptionClients(t)
	_ = kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	needsUpdate := apiServer.Spec.Encryption.Type != encryptionType
	if needsUpdate {
		apiServer.Spec.Encryption.Type = encryptionType
		_, err = apiServerClient.Update(apiServer)
		require.NoError(t, err)
	}

	test.WaitForKubeAPIServerClusterOperatorAvailableNotProgressingNotDegraded(t, configClient)
	return kv, done
}

func getEncryptionClients(t *testing.T) (test.EtcdGetter, func(), configv1client.ConfigV1Interface, configv1client.APIServerInterface, kubernetes.Interface, v1helpers.StaticPodOperatorClient) {
	t.Helper()

	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	configClient := configv1client.NewForConfigOrDie(kubeConfig)
	apiServerClient := configClient.APIServers()

	kubeClient := kubernetes.NewForConfigOrDie(kubeConfig)

	kv := test.NewEtcdGetter(kubeClient)

	gvr := operatorv1.GroupVersion.WithResource("kubeapiservers")
	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(kubeConfig, gvr)
	require.NoError(t, err)
	stopCh := make(chan struct{})
	dynamicInformers.Start(stopCh)

	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	require.Truef(t, dynamicInformers.WaitForCacheSync(timeout.Done())[gvr], "failed to sync cache for %s", gvr)

	done := func() {
		close(stopCh)
	}

	return kv, done, configClient, apiServerClient, kubeClient, operatorClient
}

func testRotation(t *testing.T, operatorClient v1helpers.StaticPodOperatorClient, secretsClient corev1.SecretInterface, kv test.EtcdGetter) string {
	reason := "force-rotation-" + rand.String(8)
	test.ForceKeyRotationMust(t, operatorClient, reason)
	var secretName string

	err := wait.Poll(test.WaitPollInterval, test.WaitPollTimeout, func() (done bool, err error) {
		secrets, err := secretsClient.List(metav1.ListOptions{})
		if err != nil {
			fmt.Printf("failed to list secrets: %v\n", err)
			return false, nil
		}

		secretName = ""
		count := 0
		for _, secret := range secrets.Items {
			if secret.Labels["encryption.operator.openshift.io/component"] != "openshift-kube-apiserver" {
				continue
			}
			if secret.Annotations["encryption.operator.openshift.io/external-reason"] == reason {
				if len(secret.Annotations["encryption.operator.openshift.io/migrated-timestamp"]) == 0 {
					fmt.Printf("secret %s with reason %s not yet migrated\n", secret.Name, reason)
					continue
				}

				count++
				secretName = secret.Name
			}
		}

		if count > 1 {
			t.Fatalf("too many secrets (%d) with force rotation reason seen", count)
		}
		fmt.Printf("Saw %d migrated secrets with reason %s, name=%s\n", count, reason, secretName)

		return count == 1, nil
	})
	require.NoError(t, err)

	test.CheckEtcdSecretsAndConfigMapsMust(t, kv, test.CheckEncryptionState("aescbc"))

	keyPrefix := getKeyPrefix(t, secretName)

	err = test.CheckEtcdSecrets(kv, test.CheckEncryptionPrefix(keyPrefix)) // TODO check actual contents to match against encryption key
	require.NoError(t, err)

	err = test.CheckEtcdConfigMaps(kv, test.CheckEncryptionPrefix(keyPrefix))
	require.NoError(t, err)

	return string(keyPrefix)
}

func getKeyPrefix(t *testing.T, secretName string) []byte {
	t.Helper()

	idx := strings.LastIndex(secretName, "-")
	keyIDStr := secretName[idx+1:]

	keyID, keyIDErr := strconv.ParseUint(keyIDStr, 10, 0)
	if keyIDErr != nil {
		t.Fatal(keyIDErr)
	}

	return []byte("k8s:enc:aescbc:v1:" + strconv.FormatUint(keyID, 10) + ":")
}

// TODO add a fake upgrade and downgrade style test that adds and removes resources
