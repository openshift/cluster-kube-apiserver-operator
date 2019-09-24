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
		t.Run(strconv.Itoa(i), f)
		if t.Failed() {
			return
		}
	}
}

func testEncryptionType(t *testing.T, encryptionType configv1.EncryptionType) (test.EtcdGetter, func()) {
	t.Helper()

	kv, done, configClient, apiServerClient, _, _ := getEncryptionClients(t)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	apiServer.Spec.Encryption.Type = encryptionType
	_, err = apiServerClient.Update(apiServer)
	require.NoError(t, err)

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

func TestEncryptionRotation(t *testing.T) {
	kv, done, configClient, apiServerClient, kubeClient, operatorClient := getEncryptionClients(t)
	defer done()
	secretsClient := kubeClient.CoreV1().Secrets(operatorclient.GlobalMachineSpecifiedConfigNamespace)

	apiServer, err := apiServerClient.Get("cluster", metav1.GetOptions{})
	require.NoError(t, err)
	apiServer.Spec.Encryption.Type = configv1.EncryptionTypeAESCBC
	_, err = apiServerClient.Update(apiServer)
	require.NoError(t, err)

	secretsPrefixes := make([]string, 3)
	cmPrefixes := make([]string, 3)

	// run a few rotations and assert that migrations occur as expected and keyIDs increase
	for i := range secretsPrefixes {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			secretsKeyPrefix, cmKeyPrefix := testRotation(t, operatorClient, secretsClient, configClient, kv)
			secretsPrefixes[i] = secretsKeyPrefix
			cmPrefixes[i] = cmKeyPrefix
		})
		if t.Failed() {
			return
		}
	}

	require.Truef(t, sort.IsSorted(sort.StringSlice(secretsPrefixes)), "secret key IDs not in ascending order: %v", secretsPrefixes)
	require.Truef(t, sort.IsSorted(sort.StringSlice(cmPrefixes)), "config map key IDs not in ascending order: %v", cmPrefixes)
}

func testRotation(t *testing.T, operatorClient v1helpers.StaticPodOperatorClient, secretsClient corev1.SecretInterface, configClient configv1client.ConfigV1Interface, kv test.EtcdGetter) (string, string) {
	reason := "force-rotation-" + rand.String(8)
	test.ForceKeyRotationMust(t, operatorClient, reason)
	var resourceToName map[string]string

	err := wait.Poll(test.WaitPollInterval, test.WaitPollTimeout, func() (done bool, err error) {
		secrets, err := secretsClient.List(metav1.ListOptions{})
		if err != nil {
			fmt.Printf("failed to list secrets: %v\n", err)
			return false, nil
		}

		resourceToName = map[string]string{}
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
				resourceToName[secret.Labels["encryption.operator.openshift.io/resource"]] = secret.Name
			}
		}

		if count > 2 {
			t.Fatalf("too many secrets (%d) with force rotation reason seen", count)
		}
		fmt.Printf("Saw %d migrated secrets with reason %s, mapping=%v\n", count, reason, resourceToName)
		if count == 2 {
			_, ok1 := resourceToName["secrets"]
			_, ok2 := resourceToName["configmaps"]
			valid := ok1 && ok2
			if !valid {
				t.Fatalf("invalid secrets seen %v", resourceToName)
			}
		}

		return count == 2, nil
	})
	require.NoError(t, err)

	test.CheckEtcdSecretsAndConfigMapsMust(t, kv, test.CheckEncryptionState("aescbc"))

	secretsKeyPrefix := getKeyPrefix(t, resourceToName["secrets"])
	err = test.CheckEtcdSecrets(kv, test.CheckEncryptionPrefix(secretsKeyPrefix))
	require.NoError(t, err)

	cmKeyPrefix := getKeyPrefix(t, resourceToName["configmaps"])
	err = test.CheckEtcdConfigMaps(kv, test.CheckEncryptionPrefix(cmKeyPrefix))
	require.NoError(t, err)

	return string(secretsKeyPrefix), string(cmKeyPrefix)
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
