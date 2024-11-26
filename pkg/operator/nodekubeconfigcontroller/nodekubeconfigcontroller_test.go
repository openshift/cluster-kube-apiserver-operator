package nodekubeconfigcontroller

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/api/annotations"
	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

type configMapLister struct {
	client    *fake.Clientset
	namespace string
}

var _ corev1listers.ConfigMapNamespaceLister = &configMapLister{}
var _ corev1listers.ConfigMapLister = &configMapLister{}

func (l *configMapLister) List(selector labels.Selector) (ret []*corev1.ConfigMap, err error) {
	list, err := l.client.CoreV1().ConfigMaps(l.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})

	var items []*corev1.ConfigMap
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}

	return items, err
}

func (l *configMapLister) ConfigMaps(namespace string) corev1listers.ConfigMapNamespaceLister {
	return &configMapLister{
		client:    l.client,
		namespace: namespace,
	}
}

func (l *configMapLister) Get(name string) (*corev1.ConfigMap, error) {
	return l.client.CoreV1().ConfigMaps(l.namespace).Get(context.Background(), name, metav1.GetOptions{})
}

type secretLister struct {
	client    *fake.Clientset
	namespace string
}

var _ corev1listers.SecretNamespaceLister = &secretLister{}
var _ corev1listers.SecretLister = &secretLister{}

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

const privateKey = `
-----BEGIN PRIVATE KEY-----
MIIBVAIBADANBgkqhkiG9w0BAQEFAASCAT4wggE6AgEAAkEArvkpSCWaStPfbYr4
cCJyv8pXWnJ4K22emSrYDNcp7Dm6qjtN/lsVNuGDyWyR4cUaJYXkaD2OrZiXDzzk
BZlS3QIDAQABAkA9BZhoGPUec5XQVk8ejGUIjkC4woM2YhyVvmNq1v8/6q6V+uPw
yDEfBMapuLVY+QhyVELXFOCHA5iKxrlFHZThAiEA1XA5mlbHtrJqEZ7yI5m6+Szj
7YVzSkdSgfDZ//heAh8CIQDR3VbN9QmJRIM1yhIkP9BoWSxvXdH6QMXdC2X7Tkwj
gwIgcpbSxjLK/CIjYhx0oXpacIaSRCX+dKV//XVChPNh/T8CIQCSFscXZez2fhfs
eLb6PuXfzbuN5ryFvVM/VXDvaIi96wIgcHjUpONghaoA51XejMAxWanDiwAgRV5H
XNdFkBi4q7o=
-----END PRIVATE KEY-----` // notsecret
const publicKey = `-----BEGIN CERTIFICATE-----
MIIBfzCCASmgAwIBAgIUEEUHu1PzqJCGQ63vxVokwBxGPYwwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI0MTEyNjA4NTA0NloXDTM0MTEy
NDA4NTA0NlowFDESMBAGA1UEAwwJbG9jYWxob3N0MFwwDQYJKoZIhvcNAQEBBQAD
SwAwSAJBAK75KUglmkrT322K+HAicr/KV1pyeCttnpkq2AzXKew5uqo7Tf5bFTbh
g8lskeHFGiWF5Gg9jq2Ylw885AWZUt0CAwEAAaNTMFEwHQYDVR0OBBYEFJna5Io+
idLKO73zypGl2itp92JUMB8GA1UdIwQYMBaAFJna5Io+idLKO73zypGl2itp92JU
MA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZIhvcNAQELBQADQQB71tlkWNFDvMRxtz+a
NYMU1thAVfVFciNXPS07tUduFSwVvYORUxx2w+5JfUdKu69hLpBFVPqvHQjPoQgc
vUBI
-----END CERTIFICATE-----`
const certNotBefore = "2024-11-26T08:50:46Z"
const certNotAfter = "2034-11-24T08:50:46Z"

func TestEnsureNodeKubeconfigs(t *testing.T) {
	publicKeyBase64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	privateKeyBase64 := base64.StdEncoding.EncodeToString([]byte(privateKey))
	tt := []struct {
		name            string
		existingObjects []runtime.Object
		infrastructure  *configv1.Infrastructure
		expectedErr     error
		expectedActions []clienttesting.Action
	}{
		{
			name: "all required info present",
			existingObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "openshift-kube-apiserver",
						Name:      "kube-apiserver-server-ca",
					},
					Data: map[string]string{
						"ca-bundle.crt": "kube-apiserver-server-ca certificate",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "openshift-kube-apiserver-operator",
						Name:      "node-system-admin-client",
					},
					Data: map[string][]byte{
						"tls.crt": []byte(publicKey),
						"tls.key": []byte(privateKey),
					},
				},
			},
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "",
					Name:      "cluster",
				},
				Status: configv1.InfrastructureStatus{
					APIServerURL:         "https://lb-ext.test:6443",
					APIServerInternalURL: "https://lb-int.test:6443",
				},
			},
			expectedErr: nil,
			expectedActions: []clienttesting.Action{
				clienttesting.CreateActionImpl{
					ActionImpl: clienttesting.ActionImpl{
						Namespace: "openshift-kube-apiserver",
						Verb:      "create",
						Resource:  corev1.SchemeGroupVersion.WithResource("secrets"),
					},
					Object: &corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "openshift-kube-apiserver",
							Name:      "node-kubeconfigs",
							Annotations: map[string]string{
								annotations.OpenShiftComponent:              "kube-apiserver",
								certrotation.CertificateNotBeforeAnnotation: certNotBefore,
								certrotation.CertificateNotAfterAnnotation:  certNotAfter,
							},
						},
						Data: map[string][]byte{
							"localhost.kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: a3ViZS1hcGlzZXJ2ZXItc2VydmVyLWNhIGNlcnRpZmljYXRl
    server: https://localhost:6443
  name: localhost
contexts:
- context:
    cluster: localhost
    user: system:admin
  name: system:admin
current-context: system:admin
users:
- name: system:admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, publicKeyBase64, privateKeyBase64)),
							"localhost-recovery.kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: a3ViZS1hcGlzZXJ2ZXItc2VydmVyLWNhIGNlcnRpZmljYXRl
    server: https://localhost:6443
    tls-server-name: localhost-recovery
  name: localhost-recovery
contexts:
- context:
    cluster: localhost-recovery
    user: system:admin
  name: system:admin
current-context: system:admin
users:
- name: system:admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, publicKeyBase64, privateKeyBase64)),
							"lb-ext.kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: a3ViZS1hcGlzZXJ2ZXItc2VydmVyLWNhIGNlcnRpZmljYXRl
    server: https://lb-ext.test:6443
  name: lb-ext
contexts:
- context:
    cluster: lb-ext
    user: system:admin
  name: system:admin
current-context: system:admin
users:
- name: system:admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, publicKeyBase64, privateKeyBase64)),
							"lb-int.kubeconfig": []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: a3ViZS1hcGlzZXJ2ZXItc2VydmVyLWNhIGNlcnRpZmljYXRl
    server: https://lb-int.test:6443
  name: lb-int
contexts:
- context:
    cluster: lb-int
    user: system:admin
  name: system:admin
current-context: system:admin
users:
- name: system:admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, publicKeyBase64, privateKeyBase64)),
						},
					},
				},
			},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset(tc.existingObjects...)

			infraIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tc.infrastructure != nil {
				err := infraIndexer.Add(tc.infrastructure)
				if err != nil {
					t.Fatal(err)
				}
			}
			infraLister := configlistersv1.NewInfrastructureLister(infraIndexer)

			err := ensureNodeKubeconfigs(
				context.Background(),
				kubeClient.CoreV1(),
				&secretLister{client: kubeClient, namespace: ""},
				&configMapLister{client: kubeClient, namespace: ""},
				infraLister,
				events.NewInMemoryRecorder(t.Name(), clock.RealClock{}),
			)
			if err != tc.expectedErr {
				t.Fatalf("expected err %v, got %v", tc.expectedErr, err)
			}

			// filter out GET requests
			var actions []clienttesting.Action
			for _, a := range kubeClient.Actions() {
				if a.GetVerb() == "get" {
					continue
				}
				actions = append(actions, a)
			}
			if !apiequality.Semantic.DeepEqual(actions, tc.expectedActions) {
				t.Errorf("expected and real actions differ %s", cmp.Diff(tc.expectedActions, actions,
					cmp.Transformer("Data", func(in map[string][]byte) map[string]string {
						out := make(map[string]string, len(in))
						for k, v := range in {
							out[k] = string(v)
						}
						return out
					})))
			}
		})
	}
}
