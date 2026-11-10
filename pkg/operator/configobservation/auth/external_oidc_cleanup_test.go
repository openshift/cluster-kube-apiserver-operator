package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
	"sigs.k8s.io/yaml"
)

// newDirectOIDCCleanupObjects represents a completed webhook revision. Node
// rollout status is deliberately absent: running pods have revisioned copies.
func newDirectOIDCCleanupObjects(t *testing.T) (map[string]*corev1.ConfigMap, *corev1.Secret) {
	t.Helper()
	raw, err := yaml.Marshal(prunedBaseWebhookAuthenticatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	configMaps := map[string]*corev1.ConfigMap{}
	for _, name := range []string{"config", "config-2"} {
		configMaps[name] = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: name},
			Data:       map[string]string{"config.yaml": string(raw)},
		}
	}
	configMaps["revision-status-2"] = cleanupRevisionStatus(2, true)
	return configMaps, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "webhook-authenticator-2"},
		Data:       map[string][]byte{"kubeConfig": correctKubeConfigString},
	}
}

func TestObserveAuthenticationMigrationAndCleanup(t *testing.T) {
	for _, authType := range []configv1.AuthenticationType{configv1.AuthenticationTypeOIDC, configv1.AuthenticationTypeIntegratedOAuth} {
		t.Run(string(authType), func(t *testing.T) {
			configMaps, revisionedSecret := newDirectOIDCCleanupObjects(t)
			direct, err := yaml.Marshal(baseConfig)
			if err != nil {
				t.Fatal(err)
			}
			webhookConfig := configMaps["config"].DeepCopy()
			configMaps["config"].Data["config.yaml"] = string(direct)
			configMaps["config-2"].Data["config.yaml"] = string(direct)
			cmIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, cm := range configMaps {
				if err := cmIndexer.Add(cm); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmIndexer.Add(baseTargetConfigMap.DeepCopy()); err != nil {
				t.Fatal(err)
			}
			secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, location := range []struct{ namespace, name string }{
				{operatorclient.GlobalUserSpecifiedConfigNamespace, defaultWebhookSecretName},
				{operatorclient.TargetNamespace, "webhook-authenticator"},
			} {
				secret := revisionedSecret.DeepCopy()
				secret.Namespace, secret.Name = location.namespace, location.name
				if err := secretIndexer.Add(secret); err != nil {
					t.Fatal(err)
				}
			}
			authIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			auth := authResourceWithOIDC.DeepCopy()
			auth.Spec.Type = authType
			if err := authIndexer.Add(auth); err != nil {
				t.Fatal(err)
			}
			syncer := &mockResourceSyncer{t: t}
			listers := configobservation.Listers{
				AuthConfigLister:    configlistersv1.NewAuthenticationLister(authIndexer),
				ConfigmapLister_:    corelistersv1.NewConfigMapLister(cmIndexer),
				ConfigSecretLister_: corelistersv1.NewSecretLister(secretIndexer),
				SecretLister_:       corelistersv1.NewSecretLister(secretIndexer),
				ResourceSync:        syncer,
			}
			observe := NewAuthenticationObserver(featureGatesWithWebhook)
			recorder := events.NewInMemoryRecorder("migration-cleanup", clock.RealClock{})
			existing := runtime.DeepCopyJSON(baseConfig)
			for _, step := range []struct {
				name      string
				cm        *corev1.ConfigMap
				secret    *corev1.Secret
				canDelete bool
			}{
				{name: "publish webhook arguments"},
				{name: "next observation still sees rendered direct OIDC"},
				{name: "renderer publishes webhook but latest revision is direct", cm: webhookConfig},
				{name: "revision starts", cm: cleanupRevisionStatus(3, false)},
				{name: "revision copies webhook config", cm: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "config-3"}, Data: webhookConfig.Data}},
				{name: "revision completes before Secret cache catches up", cm: cleanupRevisionStatus(3, true)},
				{name: "revisioned Secret is observed", secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "webhook-authenticator-3"}, Data: revisionedSecret.Data}, canDelete: true},
			} {
				t.Run(step.name, func(t *testing.T) {
					if step.cm != nil {
						if err := cmIndexer.Update(step.cm); err != nil {
							t.Fatal(err)
						}
					}
					if step.secret != nil {
						if err := secretIndexer.Add(step.secret); err != nil {
							t.Fatal(err)
						}
					}
					syncer.synced = map[string]string{}
					observed, errs := observe(listers, recorder, existing)
					if len(errs) != 0 {
						t.Fatalf("unexpected errors: %v", errs)
					}
					if !equality.Semantic.DeepEqual(prunedBaseWebhookAuthenticatorConfig, observed) {
						t.Fatalf("expected webhook-only configuration, got %v", observed)
					}
					deleted := syncer.synced["configmap/auth-config.openshift-kube-apiserver"] == "DELETE"
					if deleted != step.canDelete {
						t.Errorf("expected deletion %v, got sync requests %v", step.canDelete, syncer.synced)
					}
					existing = observed
				})
			}
		})
	}
}

// cleanupRevisionStatus builds the marker written before and after revision assembly.
func cleanupRevisionStatus(revision int, ready bool) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   operatorclient.TargetNamespace,
			Name:        fmt.Sprintf("revision-status-%d", revision),
			Annotations: map[string]string{"operator.openshift.io/revision-ready": fmt.Sprint(ready)},
		},
		Data: map[string]string{"revision": fmt.Sprint(revision)},
	}
}

func TestObserveExternalOIDCCleanup(t *testing.T) {
	directConfig, err := yaml.Marshal(baseConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, gates := range []struct {
		name   string
		access featuregates.FeatureGateAccess
	}{
		{"webhook only", featureGatesWithWebhook},
		{"claims sourcing only", featureGatesWithExternalClaimsSourcing},
		{"both gates", featureGatesWithWebhookAndExternalClaimsSourcing},
	} {
		t.Run(gates.name, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				mutate        func(map[string]*corev1.ConfigMap, *corev1.Secret)
				missingSecret bool
				secretError   bool
				listError     bool
				missingTarget bool
				canDelete     bool
				errorContains string
			}{
				{name: "completed webhook revision allows deletion before node rollout", canDelete: true},
				{name: "ConfigMap snapshot fails", listError: true, errorContains: "listing kube-apiserver ConfigMaps"},
				{name: "nothing to clean up", missingTarget: true, missingSecret: true},
				{name: "no revision status", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) { delete(cms, "revision-status-2") }},
				{name: "rendered config missing", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) { delete(cms, "config") }},
				{name: "rendered config still uses direct OIDC", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config"].Data["config.yaml"] = string(directConfig)
				}},
				{name: "revision config missing", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) { delete(cms, "config-2") }},
				{name: "completed revision still uses direct OIDC", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config-2"].Data["config.yaml"] = string(directConfig)
				}},
				{name: "revision unfinished", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-2"] = cleanupRevisionStatus(2, false)
				}},
				{name: "newer unfinished revision with old direct config", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-3"] = cleanupRevisionStatus(3, false)
					cms["config-3"] = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "config-3"}, Data: map[string]string{"config.yaml": string(directConfig)}}
				}},
				{name: "newer unfinished revision before copying config", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-3"] = cleanupRevisionStatus(3, false)
				}},
				{name: "old unfinished revision does not block newer completed revision", canDelete: true, mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-1"] = cleanupRevisionStatus(1, false)
				}},
				{name: "newer completed revision still needs direct OIDC", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-3"] = cleanupRevisionStatus(3, true)
					cms["config-3"] = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "config-3"}, Data: map[string]string{"config.yaml": string(directConfig)}}
				}},
				{name: "revision metadata invalid", errorContains: "invalid revision metadata", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["revision-status-2"].Data["revision"] = "3"
				}},
				{name: "missing config.yaml", errorContains: "missing nonempty config.yaml", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) { delete(cms["config"].Data, "config.yaml") }},
				{name: "invalid config.yaml", errorContains: "parsing configmap", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config"].Data["config.yaml"] = "apiServerArguments: ["
				}},
				{name: "malformed direct argument", errorContains: "reading direct OIDC arguments", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config"].Data["config.yaml"] = "apiServerArguments:\n  authentication-config: unexpected-scalar\n"
				}},
				{name: "no replacement webhook", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) { cms["config"].Data["config.yaml"] = "{}" }},
				{name: "wrong webhook file", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config-2"].Data["config.yaml"] = strings.ReplaceAll(cms["config-2"].Data["config.yaml"], "webhook-authenticator/kubeConfig", "other/kubeConfig")
				}},
				{name: "wrong webhook version", mutate: func(cms map[string]*corev1.ConfigMap, _ *corev1.Secret) {
					cms["config-2"].Data["config.yaml"] = strings.ReplaceAll(cms["config-2"].Data["config.yaml"], "- v1", "- v1beta1")
				}},
				{name: "revisioned Secret missing", missingSecret: true},
				{name: "revisioned Secret lookup fails", secretError: true, errorContains: "getting secret openshift-kube-apiserver/webhook-authenticator-2"},
				{name: "revisioned Secret invalid", errorContains: "secret openshift-kube-apiserver/webhook-authenticator-2 is invalid", mutate: func(_ map[string]*corev1.ConfigMap, secret *corev1.Secret) {
					secret.Data["kubeConfig"] = []byte("invalid")
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cms, secret := newDirectOIDCCleanupObjects(t)
					if tc.mutate != nil {
						tc.mutate(cms, secret)
					}
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
					for _, cm := range cms {
						if err := indexer.Add(cm); err != nil {
							t.Fatal(err)
						}
					}
					if !tc.missingTarget {
						if err := indexer.Add(baseTargetConfigMap.DeepCopy()); err != nil {
							t.Fatal(err)
						}
					}
					var secretIndexer cache.Indexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					if tc.secretError {
						secretIndexer = &everFailingIndexer{}
					} else if !tc.missingSecret {
						if err := secretIndexer.Add(secret); err != nil {
							t.Fatal(err)
						}
					}
					synced := map[string]string{}
					var configMapLister corelistersv1.ConfigMapLister = corelistersv1.NewConfigMapLister(indexer)
					if tc.listError {
						configMapLister = &cleanupListErrorLister{ConfigMapLister: configMapLister}
					}
					listers := configobservation.Listers{
						ConfigmapLister_: configMapLister,
						SecretLister_:    corelistersv1.NewSecretLister(secretIndexer),
						ResourceSync:     &mockResourceSyncer{t: t, synced: synced},
					}
					recorder := events.NewInMemoryRecorder("cleanup", clock.RealClock{})
					observe := NewObserveExternalOIDC(gates.access)
					config, errs := observe(listers, recorder, runtime.DeepCopyJSON(prunedBaseWebhookAuthenticatorConfig))
					if len(config) != 0 {
						t.Errorf("cleanup must not restore direct OIDC arguments: %v", config)
					}
					if tc.errorContains == "" {
						if len(errs) != 0 {
							t.Errorf("unexpected errors: %v", errs)
						}
					} else if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.errorContains) {
						t.Errorf("expected error containing %q, got %v", tc.errorContains, errs)
					}
					deleted := synced["configmap/auth-config.openshift-kube-apiserver"] == "DELETE"
					if deleted != tc.canDelete {
						t.Errorf("expected deletion %v, got sync requests %v", tc.canDelete, synced)
					}
				})
			}
		})
	}
}

// cleanupListErrorLister preserves individual Gets while failing the snapshot List.
type cleanupListErrorLister struct {
	corelistersv1.ConfigMapLister
}

func (l *cleanupListErrorLister) ConfigMaps(namespace string) corelistersv1.ConfigMapNamespaceLister {
	return &cleanupNamespaceListErrorLister{ConfigMapNamespaceLister: l.ConfigMapLister.ConfigMaps(namespace)}
}

type cleanupNamespaceListErrorLister struct {
	corelistersv1.ConfigMapNamespaceLister
}

func (l *cleanupNamespaceListErrorLister) List(labels.Selector) ([]*corev1.ConfigMap, error) {
	return nil, errors.New("snapshot unavailable")
}
