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

const privateKey = "fake private key" // notsecret
const publicKey = "fake public key"
const certNotBefore = "2024-11-26T08:50:46Z"
const certNotAfter = "2034-11-24T08:50:46Z"
const lbExtServer = "https://lb-ext.test:6443"
const lbIntServer = "https://lb-int.test:6443"

var publicKeyBase64 = base64.StdEncoding.EncodeToString([]byte(publicKey))
var privateKeyBase64 = base64.StdEncoding.EncodeToString([]byte(privateKey))

func generateKubeConfig(name, server string) []byte {
	// localhost-recovery is a special case, it also has tls-server-name set
	tlsServerName := ""
	if name == "localhost-recovery" {
		tlsServerName = `
    tls-server-name: localhost-recovery`
	}
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: a3ViZS1hcGlzZXJ2ZXItc2VydmVyLWNhIGNlcnRpZmljYXRl
    server: %s%s
  name: %s
contexts:
- context:
    cluster: %s
    user: system:admin
  name: system:admin
current-context: system:admin
users:
- name: system:admin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, server, tlsServerName, name, name, publicKeyBase64, privateKeyBase64))
}

func TestEnsureNodeKubeconfigs(t *testing.T) {
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
						Annotations: map[string]string{
							certrotation.CertificateNotBeforeAnnotation: certNotBefore,
							certrotation.CertificateNotAfterAnnotation:  certNotAfter,
						},
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
					APIServerURL:         lbExtServer,
					APIServerInternalURL: lbIntServer,
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
							"localhost.kubeconfig":          generateKubeConfig("localhost", "https://localhost:6443"),
							"localhost-recovery.kubeconfig": generateKubeConfig("localhost-recovery", "https://localhost:6443"),
							"lb-ext.kubeconfig":             generateKubeConfig("lb-ext", lbExtServer),
							"lb-int.kubeconfig":             generateKubeConfig("lb-int", lbIntServer),
						},
					},
				},
			},
		}, {
			name: "no annotations set",
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
					APIServerURL:         lbExtServer,
					APIServerInternalURL: lbIntServer,
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
								annotations.OpenShiftComponent: "kube-apiserver",
							},
						},
						Data: map[string][]byte{
							"localhost.kubeconfig":          generateKubeConfig("localhost", "https://localhost:6443"),
							"localhost-recovery.kubeconfig": generateKubeConfig("localhost-recovery", "https://localhost:6443"),
							"lb-ext.kubeconfig":             generateKubeConfig("lb-ext", lbExtServer),
							"lb-int.kubeconfig":             generateKubeConfig("lb-int", lbIntServer),
						},
					},
				},
			},
		}, {
			name: "update",
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
						Annotations: map[string]string{
							certrotation.CertificateNotBeforeAnnotation: certNotBefore,
							certrotation.CertificateNotAfterAnnotation:  certNotAfter,
						},
					},
					Data: map[string][]byte{
						"tls.crt": []byte(publicKey),
						"tls.key": []byte(privateKey),
					},
				},
				&corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Secret",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "openshift-kube-apiserver",
						Name:      "node-kubeconfigs",
						Annotations: map[string]string{
							annotations.OpenShiftComponent:              "kube-apiserver",
							certrotation.CertificateNotBeforeAnnotation: "some-old-not-before",
							certrotation.CertificateNotAfterAnnotation:  "some-old-not-after",
						},
					},
					Data: map[string][]byte{
						"localhost.kubeconfig":          []byte("invalid kubeconfig"),
						"localhost-recovery.kubeconfig": []byte("another invalid kubeconfig"),
						"lb-ext.kubeconfig":             []byte("more invalid kubeconfig"),
						"lb-int.kubeconfig":             []byte("even more invalid kubeconfig"),
					},
				},
			},
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "",
					Name:      "cluster",
				},
				Status: configv1.InfrastructureStatus{
					APIServerURL:         lbExtServer,
					APIServerInternalURL: lbIntServer,
				},
			},
			expectedErr: nil,
			expectedActions: []clienttesting.Action{
				clienttesting.DeleteActionImpl{
					ActionImpl: clienttesting.ActionImpl{
						Namespace: "openshift-kube-apiserver",
						Verb:      "delete",
						Resource:  corev1.SchemeGroupVersion.WithResource("secrets"),
					},
					Name: "node-kubeconfigs",
				},
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
							Namespace:       "openshift-kube-apiserver",
							Name:            "node-kubeconfigs",
							Labels:          map[string]string{},
							OwnerReferences: []metav1.OwnerReference{},
							Annotations: map[string]string{
								annotations.OpenShiftComponent:              "kube-apiserver",
								certrotation.CertificateNotBeforeAnnotation: certNotBefore,
								certrotation.CertificateNotAfterAnnotation:  certNotAfter,
							},
						},
						Data: map[string][]byte{
							"localhost.kubeconfig":          generateKubeConfig("localhost", "https://localhost:6443"),
							"localhost-recovery.kubeconfig": generateKubeConfig("localhost-recovery", "https://localhost:6443"),
							"lb-ext.kubeconfig":             generateKubeConfig("lb-ext", lbExtServer),
							"lb-int.kubeconfig":             generateKubeConfig("lb-int", lbIntServer),
						},
						Type: corev1.SecretTypeOpaque,
					},
				},
			},
		},
		{
			name: "annotations only update",
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
						Annotations: map[string]string{
							certrotation.CertificateNotBeforeAnnotation: certNotBefore,
							certrotation.CertificateNotAfterAnnotation:  certNotAfter,
						},
					},
					Data: map[string][]byte{
						"tls.crt": []byte(publicKey),
						"tls.key": []byte(privateKey),
					},
				},
				&corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Secret",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "openshift-kube-apiserver",
						Name:      "node-kubeconfigs",
						Annotations: map[string]string{
							annotations.OpenShiftComponent:              "kube-apiserver",
							certrotation.CertificateNotBeforeAnnotation: "some-old-not-before",
							certrotation.CertificateNotAfterAnnotation:  "some-old-not-after",
						},
					},
					Data: map[string][]byte{
						"localhost.kubeconfig":          generateKubeConfig("localhost", "https://localhost:6443"),
						"localhost-recovery.kubeconfig": generateKubeConfig("localhost-recovery", "https://localhost:6443"),
						"lb-ext.kubeconfig":             generateKubeConfig("lb-ext", lbExtServer),
						"lb-int.kubeconfig":             generateKubeConfig("lb-int", lbIntServer),
					},
				},
			},
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "",
					Name:      "cluster",
				},
				Status: configv1.InfrastructureStatus{
					APIServerURL:         lbExtServer,
					APIServerInternalURL: lbIntServer,
				},
			},
			expectedErr: nil,
			expectedActions: []clienttesting.Action{
				clienttesting.DeleteActionImpl{
					ActionImpl: clienttesting.ActionImpl{
						Namespace: "openshift-kube-apiserver",
						Verb:      "delete",
						Resource:  corev1.SchemeGroupVersion.WithResource("secrets"),
					},
					Name: "node-kubeconfigs",
				},
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
							Namespace:       "openshift-kube-apiserver",
							Name:            "node-kubeconfigs",
							Labels:          map[string]string{},
							OwnerReferences: []metav1.OwnerReference{},
							Annotations: map[string]string{
								annotations.OpenShiftComponent:              "kube-apiserver",
								certrotation.CertificateNotBeforeAnnotation: certNotBefore,
								certrotation.CertificateNotAfterAnnotation:  certNotAfter,
							},
						},
						Data: map[string][]byte{
							"localhost.kubeconfig":          generateKubeConfig("localhost", "https://localhost:6443"),
							"localhost-recovery.kubeconfig": generateKubeConfig("localhost-recovery", "https://localhost:6443"),
							"lb-ext.kubeconfig":             generateKubeConfig("lb-ext", lbExtServer),
							"lb-int.kubeconfig":             generateKubeConfig("lb-int", lbIntServer),
						},
						Type: corev1.SecretTypeOpaque,
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
