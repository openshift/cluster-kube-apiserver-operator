package library

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"

	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

var protoEncodingPrefix = []byte{0x6b, 0x38, 0x73, 0x00}

const (
	jsonEncodingPrefix           = "{"
	protoEncryptedDataPrefix     = "k8s:enc:"
	aesCBCTransformerPrefixV1    = "k8s:enc:aescbc:v1:"
	secretboxTransformerPrefixV1 = "k8s:enc:secretbox:v1:"
)

type EtcdGetter interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

func NewEtcdGetter(kubeClient kubernetes.Interface) EtcdGetter {
	return &etcdGetter{kubeClient: kubeClient}
}

type etcdGetter struct {
	kubeClient kubernetes.Interface
}

func (e *etcdGetter) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	// we need to rebuild this port-forward based client every time so we can tolerate API server rollouts
	kv, done, err := e.newEtcdKV()
	if err != nil {
		return nil, fmt.Errorf("failed to build port-forward based etcd client: %v", err)
	}
	defer done()
	return kv.Get(ctx, key, opts...)
}

func (e *etcdGetter) newEtcdKV() (EtcdGetter, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "oc", "port-forward", "service/etcd", ":2379", "-n", "openshift-etcd")

	done := func() {
		cancel()
		_ = cmd.Wait() // wait to clean up resources but ignore returned error since cancel kills the process
	}

	var err error // so we can clean up on error
	defer func() {
		if err != nil {
			done()
		}
	}()

	stdOut, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}

	if err = cmd.Start(); err != nil {
		return nil, nil, err
	}

	scanner := bufio.NewScanner(stdOut)
	if !scanner.Scan() {
		return nil, nil, fmt.Errorf("failed to scan port forward std out")
	}
	if err = scanner.Err(); err != nil {
		return nil, nil, err
	}
	output := scanner.Text()

	port := strings.TrimSuffix(strings.TrimPrefix(output, "Forwarding from 127.0.0.1:"), " -> 2379")
	_, err = strconv.Atoi(port)
	if err != nil {
		return nil, nil, fmt.Errorf("port forward output not in expected format: %s", output)
	}

	coreV1 := e.kubeClient.CoreV1()
	etcdConfigMap, err := coreV1.ConfigMaps("openshift-config").Get("etcd-ca-bundle", metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	etcdSecret, err := coreV1.Secrets("openshift-config").Get("etcd-client", metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	tlsConfig, err := restclient.TLSConfigFor(&restclient.Config{
		TLSClientConfig: restclient.TLSClientConfig{
			CertData: etcdSecret.Data[corev1.TLSCertKey],
			KeyData:  etcdSecret.Data[corev1.TLSPrivateKeyKey],
			CAData:   []byte(etcdConfigMap.Data["ca-bundle.crt"]),
		},
	})
	if err != nil {
		return nil, nil, err
	}

	etcdClient3, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"https://127.0.0.1:" + port},
		DialTimeout: 30 * time.Second,
		TLS:         tlsConfig,
	})
	if err != nil {
		return nil, nil, err
	}

	return etcdClient3.KV, done, nil
}

func CheckEncryptionState(expectedMode string) func([]byte) error {
	return func(data []byte) error {
		if len(data) == 0 {
			return fmt.Errorf("empty data")
		}

		expectedEncrypted := true
		switch expectedMode {
		case "identity-json", "identity-proto":
			expectedEncrypted = false
		}

		actualMode, isEncrypted := determineEncryptionMode(data)
		if expectedEncrypted != isEncrypted {
			return fmt.Errorf("unexpected encrypted state=%v, mode=%s", isEncrypted, actualMode)
		}
		if actualMode != expectedMode {
			return fmt.Errorf("unexpected mode %s", actualMode)
		}

		return nil
	}
}

func CheckEncryptionPrefix(expectedPrefix []byte) func([]byte) error {
	return func(data []byte) error {
		if len(data) == 0 {
			return fmt.Errorf("empty data")
		}

		if !hasPrefixAndTrailingData(data, expectedPrefix) {
			return fmt.Errorf("invalid prefix seen")
		}

		return nil
	}
}

func determineEncryptionMode(data []byte) (string, bool) {
	isEncrypted := bytes.HasPrefix(data, []byte(protoEncryptedDataPrefix)) // all encrypted data has this prefix
	return func() string {
		switch {
		case hasPrefixAndTrailingData(data, []byte(aesCBCTransformerPrefixV1)): // AES-CBC has this prefix
			return "aescbc"
		case hasPrefixAndTrailingData(data, []byte(secretboxTransformerPrefixV1)): // Secretbox has this prefix
			return "secretbox"
		case hasPrefixAndTrailingData(data, []byte(jsonEncodingPrefix)): // unencrypted json data has this prefix
			return "identity-json"
		case hasPrefixAndTrailingData(data, protoEncodingPrefix): // unencrypted protobuf data has this prefix
			return "identity-proto"
		default:
			return "unknown" // this should never happen
		}
	}(), isEncrypted
}

func hasPrefixAndTrailingData(data, prefix []byte) bool {
	return bytes.HasPrefix(data, prefix) && len(data) > len(prefix)
}

func CheckEtcdSecretsAndConfigMapsMust(t *testing.T, kv EtcdGetter, f func([]byte) error) {
	t.Helper()
	err := CheckEtcdSecrets(kv, f)
	require.NoError(t, err)
	err = CheckEtcdConfigMaps(kv, f)
	require.NoError(t, err)
}

func CheckEtcdSecrets(kv EtcdGetter, f func([]byte) error) error {
	return CheckEtcdList(kv, "/kubernetes.io/secrets/", f)
}

func CheckEtcdConfigMaps(kv EtcdGetter, f func([]byte) error) error {
	return CheckEtcdList(kv, "/kubernetes.io/configmaps/", f)
}

func CheckEtcdList(kv EtcdGetter, keyPrefix string, f func([]byte) error) error {
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	resp, err := kv.Get(timeout, keyPrefix, clientv3.WithPrefix())
	switch {
	case err != nil:
		return fmt.Errorf("failed to list prefix %s: %v", keyPrefix, err)
	case resp.Count == 0 || len(resp.Kvs) == 0:
		return fmt.Errorf("empty list response for prefix %s: %+v", keyPrefix, resp)
	case resp.More:
		return fmt.Errorf("incomplete list response for prefix %s: %+v", keyPrefix, resp)
	}

	for _, keyValue := range resp.Kvs {
		if err := f(keyValue.Value); err != nil {
			return fmt.Errorf("key %s failed check: %v\n%s", keyValue.Key, err, hex.Dump(keyValue.Value))
		}
	}

	return nil
}

func ForceKeyRotationMust(t *testing.T, operatorClient v1helpers.StaticPodOperatorClient, reason string) {
	t.Helper()
	require.NoError(t, ForceKeyRotation(operatorClient, reason))
}

func ForceKeyRotation(operatorClient v1helpers.StaticPodOperatorClient, reason string) error {
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
		operatorSpec, _, resourceVersion, err := operatorClient.GetStaticPodOperatorStateWithQuorum()
		if err != nil {
			return err
		}

		operatorSpec = operatorSpec.DeepCopy()
		operatorSpec.UnsupportedConfigOverrides.Raw = raw

		_, _, err = operatorClient.UpdateStaticPodOperatorSpec(resourceVersion, operatorSpec)
		return err
	})
}
