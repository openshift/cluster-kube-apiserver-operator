package targetconfigcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption/secrets"
	"github.com/openshift/library-go/pkg/operator/encryption/state"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

func TestAddKMSPluginToPodSpec(t *testing.T) {
	tests := []struct {
		name                string
		podSpec             *corev1.PodSpec
		expectedPodSpec     *corev1.PodSpec
		featureGateAccessor featuregates.FeatureGateAccess
		secrets             []*corev1.Secret
		wantErr             string
	}{
		{
			name: "happy path: KMS sidecar and volumes are injected",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: expectedPodSpecWithKMSSidecar(
				&state.VaultProviderConfig{
					Image:          "quay.io/bertinatto/vault:v2",
					VaultAddress:   "https://vault.example.com:8200",
					VaultNamespace: "my-namespace",
					TransitKey:     "my-key",
					TransitMount:   "transit",
				},
				"unix:///var/run/kmsplugin/kms-555.sock",
				newVaultCredentialsSecret("test-role-id", "test-secret-id", "my-key", "https://vault.example.com:8200", "my-namespace"),
			),
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				newEncryptionConfigSecret(t,
					"555_secrets",
					"unix:///var/run/kmsplugin/kms-555.sock",
					&state.KMSProviderConfig{
						Vault: &state.VaultProviderConfig{
							Image:          "quay.io/bertinatto/vault:v2",
							VaultAddress:   "https://vault.example.com:8200",
							VaultNamespace: "my-namespace",
							TransitKey:     "my-key",
							TransitMount:   "transit",
						},
					},
					"555",
				),
				newVaultCredentialsSecret("test-role-id", "test-secret-id", "my-key", "https://vault.example.com:8200", "my-namespace"),
			},
		},
		{
			name: "different key ID: KMS sidecar injected correctly",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: expectedPodSpecWithKMSSidecar(
				&state.VaultProviderConfig{
					Image:          "quay.io/bertinatto/vault:v2",
					VaultAddress:   "https://vault.example.com:8200",
					VaultNamespace: "my-namespace",
					TransitKey:     "my-key",
					TransitMount:   "transit",
				},
				"unix:///var/run/kmsplugin/kms-3.sock",
				newVaultCredentialsSecret("test-role-id", "test-secret-id", "my-key", "https://vault.example.com:8200", "my-namespace"),
			),
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				newEncryptionConfigSecret(t,
					"3_secrets",
					"unix:///var/run/kmsplugin/kms-3.sock",
					&state.KMSProviderConfig{
						Vault: &state.VaultProviderConfig{
							Image:          "quay.io/bertinatto/vault:v2",
							VaultAddress:   "https://vault.example.com:8200",
							VaultNamespace: "my-namespace",
							TransitKey:     "my-key",
							TransitMount:   "transit",
						},
					},
					"3",
				),
				newVaultCredentialsSecret("test-role-id", "test-secret-id", "my-key", "https://vault.example.com:8200", "my-namespace"),
			},
		},
		{
			name: "feature gate disabled: pod spec unchanged",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				nil,
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
			),
		},
		{
			name: "encryption config secret not found: pod spec unchanged",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{},
		},
		{
			name: "no KMS provider in EncryptionConfiguration: pod spec unchanged",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "encryption-config-openshift-kube-apiserver",
						Namespace: "openshift-config-managed",
					},
					Data: map[string][]byte{
						"encryption-config": []byte(`
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - identity: {}
`),
					},
				},
			},
		},
		{
			name: "vault credentials secret not found: pod spec unchanged",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				newEncryptionConfigSecret(t,
					"1_secrets",
					"unix:///var/run/kmsplugin/kms-1.sock",
					&state.KMSProviderConfig{
						Vault: &state.VaultProviderConfig{
							VaultAddress:   "https://vault.example.com:8200",
							VaultNamespace: "my-namespace",
							TransitKey:     "my-key",
							TransitMount:   "transit",
						},
					},
					"1",
				),
			},
		},
		{
			name: "malformed KMS endpoint: error",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "encryption-config-openshift-kube-apiserver",
						Namespace: "openshift-config-managed",
					},
					Data: map[string][]byte{
						"encryption-config": []byte(`
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          apiVersion: v2
          name: invalid-name
          endpoint: unix:///var/run/kmsplugin/kms.sock
          timeout: 10s
`),
					},
				},
			},
			wantErr: "unexpected KMS endpoint format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewClientset()
			for _, s := range tt.secrets {
				_, err := kubeClient.CoreV1().Secrets(s.Namespace).Create(context.Background(), s, metav1.CreateOptions{})
				require.NoError(t, err)
			}
			sl := &secretLister{client: kubeClient, namespace: ""}

			err := AddKMSPluginToPodSpec(tt.podSpec, tt.featureGateAccessor, sl)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedPodSpec, tt.podSpec)
		})
	}
}

func expectedPodSpecWithKMSSidecar(vaultConfig *state.VaultProviderConfig, endpoint string, credentials *corev1.Secret) *corev1.PodSpec {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate

	args := fmt.Sprintf(`
	echo "%s" > /tmp/secret-id
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=%s \
	-transit-key=%s \
	-log-level=debug-extended \
	-approle-role-id=%s \
	-approle-secret-id-path=/tmp/secret-id`,
		credentials.Data["VAULT_SECRET_ID"],
		endpoint,
		vaultConfig.VaultAddress,
		vaultConfig.VaultNamespace,
		vaultConfig.TransitMount,
		vaultConfig.TransitKey,
		credentials.Data["VAULT_ROLE_ID"],
	)

	privileged := true
	return &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name: "kube-apiserver",
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "kms-plugin-socket",
						MountPath: "/var/run/kmsplugin",
					},
				},
			},
			{
				Name:    "kms-plugin",
				Image:   vaultConfig.Image,
				Command: []string{"/bin/sh", "-c"},
				Args:    []string{args},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "kms-plugin-socket",
						MountPath: "/var/run/kmsplugin",
					},
				},
				SecurityContext: &corev1.SecurityContext{
					Privileged: &privileged,
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "kms-plugin-socket",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/run/kmsplugin",
						Type: &directoryOrCreate,
					},
				},
			},
		},
	}
}

func newVaultCredentialsSecret(roleID, secretID, keyName, vaultAddr, vaultNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vault-kms-credentials",
			Namespace: "openshift-config",
		},
		Data: map[string][]byte{
			"VAULT_ROLE_ID":   []byte(roleID),
			"VAULT_SECRET_ID": []byte(secretID),
			"VAULT_KEY_NAME":  []byte(keyName),
			"VAULT_ADDR":      []byte(vaultAddr),
			"VAULT_NAMESPACE": []byte(vaultNamespace),
		},
	}
}

func newEncryptionConfigSecret(t *testing.T, kmsName, endpoint string, providerConfig *state.KMSProviderConfig, keyID string) *corev1.Secret {
	t.Helper()

	encryptionConfig := fmt.Sprintf(`
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          apiVersion: v2
          name: %s
          endpoint: %s
          timeout: 10s
      - identity: {}
`, kmsName, endpoint)

	providerConfigBytes, err := json.Marshal(providerConfig)
	require.NoError(t, err)

	providerConfigKey := fmt.Sprintf("%s-%s", secrets.EncryptionSecretKMSProviderConfig, keyID)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "encryption-config-openshift-kube-apiserver",
			Namespace: "openshift-config-managed",
		},
		Data: map[string][]byte{
			"encryption-config": []byte(encryptionConfig),
			providerConfigKey:   providerConfigBytes,
		},
	}
}

type secretLister struct {
	client    *fake.Clientset
	namespace string
}

var _ corev1listers.SecretLister = &secretLister{}
var _ corev1listers.SecretNamespaceLister = &secretLister{}

func (l *secretLister) List(selector labels.Selector) (ret []*corev1.Secret, err error) {
	list, err := l.client.CoreV1().Secrets(l.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	var items []*corev1.Secret
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, err
}

func (l *secretLister) Secrets(namespace string) corev1listers.SecretNamespaceLister {
	return &secretLister{
		client:    l.client,
		namespace: namespace,
	}
}

func (l *secretLister) Get(name string) (*corev1.Secret, error) {
	return l.client.CoreV1().Secrets(l.namespace).Get(context.Background(), name, metav1.GetOptions{})
}
