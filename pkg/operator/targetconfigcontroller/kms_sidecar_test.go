package targetconfigcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
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
		kmsPluginImage      string
		wantErr             string
	}{
		{
			name: "happy path: KMS sidecar and volumes are injected",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: expectedPodSpecWithKMSSidecar("quay.io/example/vault-kms:v1", &vaultConfiguration{
				RoleID:    "test-role-id",
				Addr:      "https://vault.example.com:8200",
				Namespace: "my-namespace",
				KeyName:   "my-key",
			}, "unix:///var/run/kmsplugin/kms-555.sock", "kms-secret-id-555"),
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				newEncryptionConfigSecret(t, &vaultConfiguration{
					RoleID:    "test-role-id",
					Addr:      "https://vault.example.com:8200",
					Namespace: "my-namespace",
					KeyName:   "my-key",
				}),
			},
			kmsPluginImage: "quay.io/example/vault-kms:v1",
		},
		{
			name: "KMS provider name with extra dashes",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "kube-apiserver"},
				},
			},
			expectedPodSpec: expectedPodSpecWithKMSSidecar("quay.io/example/vault-kms:v1", &vaultConfiguration{
				RoleID:    "test-role-id",
				Addr:      "https://vault.example.com:8200",
				Namespace: "my-namespace",
				KeyName:   "my-key",
			}, "unix:///var/run/kmsplugin/kms-3.sock", "kms-secret-id-3"),
			featureGateAccessor: featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{features.FeatureGateKMSEncryption},
				nil,
			),
			secrets: []*corev1.Secret{
				newEncryptionConfigSecretWithKMSName(t, "vault-kms-3_secrets", "unix:///var/run/kmsplugin/kms-3.sock", &vaultConfiguration{
					RoleID:    "test-role-id",
					Addr:      "https://vault.example.com:8200",
					Namespace: "my-namespace",
					KeyName:   "my-key",
				}, 3),
			},
			kmsPluginImage: "quay.io/example/vault-kms:v1",
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
			name: "malformed KMS provider name: error",
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
			wantErr: "unexpected KMS provider name format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewClientset()
			for _, s := range tt.secrets {
				_, err := kubeClient.CoreV1().Secrets(s.Namespace).Create(context.Background(), s, metav1.CreateOptions{})
				require.NoError(t, err)
			}
			secretLister := &secretLister{client: kubeClient, namespace: ""}

			err := AddKMSPluginToPodSpec(tt.podSpec, tt.featureGateAccessor, secretLister, tt.kmsPluginImage)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedPodSpec, tt.podSpec)
		})
	}
}

func expectedPodSpecWithKMSSidecar(image string, config *vaultConfiguration, endpoint, keySecretID string) *corev1.PodSpec {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate
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
				Name:            "kms-plugin",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/sh", "-c"},
				Args: []string{fmt.Sprintf(`
	exec /vault-kube-kms \
	-listen-address=%s \
	-vault-address=%s \
	-vault-namespace=%s \
	-transit-mount=transit \
	-transit-key=%s \
	-log-level=debug-extended \
	-approle-role-id=%s \
	-approle-secret-id-path=/etc/kubernetes/static-pod-resources/%s`,
					endpoint,
					config.Addr,
					config.Namespace,
					config.KeyName,
					config.RoleID,
					keySecretID),
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "resource-dir",
						MountPath: "/etc/kubernetes/static-pod-resources",
					},
					{
						Name:      "kms-plugin-socket",
						MountPath: "/var/run/kmsplugin",
					},
				},
				SecurityContext: &corev1.SecurityContext{
					Privileged: new(bool),
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

// newEncryptionConfigSecret builds the "encryption-config-openshift-kube-apiserver" secret
// in "openshift-config-managed" with a valid EncryptionConfiguration containing a KMS provider
// and the corresponding kms-provider-config / kms-secret-id entries.
func newEncryptionConfigSecret(t *testing.T, config *vaultConfiguration) *corev1.Secret {
	t.Helper()

	encryptionConfig := `
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          apiVersion: v2
          name: kms-555_secrets
          endpoint: unix:///var/run/kmsplugin/kms-555.sock
          timeout: 10s
      - identity: {}
`
	providerConfig, err := json.Marshal(config)
	require.NoError(t, err)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "encryption-config-openshift-kube-apiserver",
			Namespace: "openshift-config-managed",
		},
		Data: map[string][]byte{
			"encryption-config":       []byte(encryptionConfig),
			"kms-provider-config-555": providerConfig,
			"kms-secret-id-555":       []byte("some-secret-id"),
		},
	}
}

func newEncryptionConfigSecretWithKMSName(t *testing.T, kmsName, endpoint string, config *vaultConfiguration, keyID int) *corev1.Secret {
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
	providerConfig, err := json.Marshal(config)
	require.NoError(t, err)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "encryption-config-openshift-kube-apiserver",
			Namespace: "openshift-config-managed",
		},
		Data: map[string][]byte{
			"encryption-config":                              []byte(encryptionConfig),
			fmt.Sprintf("kms-provider-config-%d", keyID):     providerConfig,
			fmt.Sprintf("kms-secret-id-%d", keyID):           []byte("some-secret-id"),
		},
	}
}

// secretLister implements corev1listers.SecretLister backed by a fake client.
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
