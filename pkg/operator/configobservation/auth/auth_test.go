package auth

import (
	"errors"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
)

func TestObserveAuthenticationMigrationSourceSecret(t *testing.T) {
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
				explicitName  *string
				secretName    string
				data          map[string][]byte
				errorContains string
			}{
				{name: "default missing", errorContains: "failed to get secret openshift-config/" + defaultWebhookSecretName},
				{name: "default valid", secretName: defaultWebhookSecretName, data: map[string][]byte{"kubeConfig": correctKubeConfigString}},
				{name: "missing key", secretName: defaultWebhookSecretName, data: map[string][]byte{}, errorContains: "missing required 'kubeConfig' key"},
				{name: "empty kubeconfig", secretName: defaultWebhookSecretName, data: map[string][]byte{"kubeConfig": {}}, errorContains: "the 'kubeConfig' key is empty"},
				{name: "invalid kubeconfig", secretName: defaultWebhookSecretName, data: map[string][]byte{"kubeConfig": []byte("not a kubeconfig")}, errorContains: "is invalid"},
				{name: "explicit managed secret valid", explicitName: ptr.To(defaultWebhookSecretName), secretName: defaultWebhookSecretName, data: map[string][]byte{"kubeConfig": correctKubeConfigString}},
				{name: "explicit managed secret missing", explicitName: ptr.To(defaultWebhookSecretName), errorContains: "failed to get secret openshift-config/" + defaultWebhookSecretName},
				{name: "custom reference rejected even with valid secret", explicitName: ptr.To("custom-webhook"), secretName: "custom-webhook", data: map[string][]byte{"kubeConfig": correctKubeConfigString}, errorContains: "spec.webhookTokenAuthenticator.kubeConfig.name must be \"" + defaultWebhookSecretName + "\" when set, got \"custom-webhook\""},
				{name: "custom reference rejected even with valid managed secret", explicitName: ptr.To("custom-webhook"), secretName: defaultWebhookSecretName, data: map[string][]byte{"kubeConfig": correctKubeConfigString}, errorContains: "cannot migrate direct OIDC to webhook"},
				{name: "explicit empty rejected", explicitName: ptr.To(""), errorContains: "spec.webhookTokenAuthenticator.kubeConfig.name must be \"" + defaultWebhookSecretName + "\" when set, got \"\""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					auth := authResourceWithOIDC.DeepCopy()
					if tc.explicitName != nil {
						auth.Spec.WebhookTokenAuthenticator = &configv1.WebhookTokenAuthenticator{
							KubeConfig: configv1.SecretNameReference{Name: *tc.explicitName},
						}
					}
					if err := indexer.Add(auth); err != nil {
						t.Fatal(err)
					}
					if tc.secretName != "" {
						if err := indexer.Add(&corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: tc.secretName},
							Data:       tc.data,
						}); err != nil {
							t.Fatal(err)
						}
					}
					synced := map[string]string{}
					listers := configobservation.Listers{
						AuthConfigLister:    configlistersv1.NewAuthenticationLister(indexer),
						ConfigSecretLister_: corelistersv1.NewSecretLister(indexer),
						SecretLister_:       corelistersv1.NewSecretLister(indexer),
						ResourceSync:        &mockResourceSyncer{t: t, synced: synced},
					}
					existing := runtime.DeepCopyJSON(baseConfig)
					observe := NewAuthenticationObserver(gates.access)
					got, errs := observe(listers, nil, existing)
					if !equality.Semantic.DeepEqual(baseConfig, got) {
						t.Errorf("source validation must preserve direct OIDC: %s", diff.Diff(baseConfig, got))
					}
					if !equality.Semantic.DeepEqual(baseConfig, existing) {
						t.Errorf("observer mutated existing config: %s", diff.Diff(baseConfig, existing))
					}
					if tc.errorContains == "" {
						if len(errs) != 0 {
							t.Errorf("unexpected errors: %v", errs)
						}
					} else if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.errorContains) {
						t.Errorf("expected error containing %q, got %v", tc.errorContains, errs)
					}
					expectedSynced := map[string]string{}
					if tc.errorContains == "" {
						expectedSynced["secret/webhook-authenticator.openshift-kube-apiserver"] = "secret/" + defaultWebhookSecretName + ".openshift-config"
					}
					if !equality.Semantic.DeepEqual(expectedSynced, synced) {
						t.Errorf("unexpected synchronization requests: %s", diff.Diff(expectedSynced, synced))
					}
				})
			}
		})
	}
}

func TestObserveAuthenticationMigrationDestinationSecret(t *testing.T) {
	syncError := errors.New("sync request failed")
	valid := map[string][]byte{"kubeConfig": correctKubeConfigString}
	stale := map[string][]byte{"kubeConfig": []byte(strings.ReplaceAll(string(correctKubeConfigString), "remote-srv-address", "previous-authenticator"))}
	if errs := validateKubeconfigSecret(&corev1.Secret{Data: stale}); len(errs) != 0 {
		t.Fatalf("stale fixture must be valid: %v", errs)
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
				data          map[string][]byte
				syncError     error
				errorContains string
				ready         bool
			}{
				{name: "missing"},
				{name: "missing kubeConfig key", data: map[string][]byte{}, errorContains: "missing required 'kubeConfig' key"},
				{name: "empty kubeConfig", data: map[string][]byte{"kubeConfig": {}}, errorContains: "the 'kubeConfig' key is empty"},
				{name: "invalid", data: map[string][]byte{"kubeConfig": []byte("not a kubeconfig")}, errorContains: "is invalid"},
				{name: "stale but valid", data: stale},
				{name: "matching kubeconfig with different metadata and unrelated data", data: map[string][]byte{"kubeConfig": correctKubeConfigString, "unrelated": []byte("ignored")}, ready: true},
				{name: "sync failure with matching destination", data: valid, syncError: syncError, errorContains: "syncing webhook-authenticator secret"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					objects := []any{
						authResourceWithOIDC.DeepCopy(),
						&corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: defaultWebhookSecretName, ResourceVersion: "100"},
							Data:       valid,
						},
					}
					if tc.data != nil {
						objects = append(objects, &corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "webhook-authenticator", ResourceVersion: "200", Annotations: map[string]string{"unrelated": "ignored"}},
							Data:       tc.data,
						})
					}
					for _, obj := range objects {
						if err := indexer.Add(obj); err != nil {
							t.Fatal(err)
						}
					}
					syncer := &mockResourceSyncer{t: t, error: tc.syncError}
					listers := configobservation.Listers{
						AuthConfigLister:    configlistersv1.NewAuthenticationLister(indexer),
						ConfigSecretLister_: corelistersv1.NewSecretLister(indexer),
						SecretLister_:       corelistersv1.NewSecretLister(indexer),
						ResourceSync:        syncer,
					}
					observe := NewAuthenticationObserver(gates.access)
					for i := 0; i < 2; i++ {
						// Each observation must request synchronization, including retries.
						syncer.synced = map[string]string{}
						existing := runtime.DeepCopyJSON(baseConfig)
						got, errs := observe(listers, nil, existing)
						expected := baseConfig
						if tc.ready {
							expected = prunedBaseWebhookAuthenticatorConfig
						}
						if !equality.Semantic.DeepEqual(expected, got) {
							t.Errorf("unexpected authentication config: %s", diff.Diff(expected, got))
						}
						if !equality.Semantic.DeepEqual(baseConfig, existing) {
							t.Errorf("observer mutated existing config: %s", diff.Diff(baseConfig, existing))
						}
						if tc.errorContains == "" {
							if len(errs) != 0 {
								t.Errorf("unexpected errors: %v", errs)
							}
						} else if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.errorContains) {
							t.Errorf("expected error containing %q, got %v", tc.errorContains, errs)
						} else if tc.syncError != nil && !errors.Is(errs[0], tc.syncError) {
							t.Errorf("expected wrapped sync error %v, got %v", tc.syncError, errs)
						} else if tc.syncError == nil && !strings.Contains(errs[0].Error(), "secret openshift-kube-apiserver/webhook-authenticator") {
							t.Errorf("destination validation error must identify the Secret: %v", errs)
						}
						expectedSynced := map[string]string{}
						if tc.syncError == nil {
							expectedSynced["secret/webhook-authenticator.openshift-kube-apiserver"] = "secret/" + defaultWebhookSecretName + ".openshift-config"
						}
						if !equality.Semantic.DeepEqual(expectedSynced, syncer.synced) {
							t.Errorf("unexpected synchronization requests: %s", diff.Diff(expectedSynced, syncer.synced))
						}
					}
				})
			}
		})
	}
}

func TestObserveAuthentication(t *testing.T) {
	oidcError := errors.New("OIDC observation failed")
	webhookError := errors.New("webhook observation failed")
	for _, tc := range []struct {
		name          string
		oidcConfig    map[string]interface{}
		webhookConfig map[string]interface{}
		oidcErrors    []error
		webhookErrors []error
		expected      map[string]interface{}
	}{
		{
			name:          "merges both observers",
			oidcConfig:    baseConfig,
			webhookConfig: unprunedBaseWebhookAuthenticatorConfig,
			expected: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"authentication-config":                    baseConfig["apiServerArguments"].(map[string]interface{})["authentication-config"],
					"authentication-token-webhook-config-file": webhookTokenAuthenticatorFile,
					"authentication-token-webhook-version":     webhookTokenAuthenticatorVersion,
				},
			},
		},
		{
			name:          "OIDC error does not prevent webhook observation",
			oidcErrors:    []error{oidcError},
			webhookConfig: prunedBaseWebhookAuthenticatorConfig,
			expected:      prunedBaseWebhookAuthenticatorConfig,
		},
		{
			name:          "webhook error preserves OIDC result",
			oidcConfig:    baseConfig,
			webhookErrors: []error{webhookError},
			expected:      baseConfig,
		},
		{
			name:          "collects both errors",
			oidcErrors:    []error{oidcError},
			webhookErrors: []error{webhookError},
			expected:      map[string]interface{}{},
		},
		{
			name:     "empty results allow authentication cleanup",
			expected: map[string]interface{}{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalExisting := runtime.DeepCopyJSON(baseConfig)
			calls := 0
			delegate := func(config map[string]interface{}, errs []error) configobserver.ObserveConfigFunc {
				return func(_ configobserver.Listers, _ events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
					calls++
					if !equality.Semantic.DeepEqual(existingConfig, baseConfig) {
						t.Errorf("delegate must receive original existing config: %s", diff.Diff(baseConfig, existingConfig))
					}
					return config, errs
				}
			}
			o := authenticationObserver{
				featureGateAccessor:  featureGatesWithOIDC,
				externalOIDCObserver: delegate(tc.oidcConfig, tc.oidcErrors),
				webhookObserver:      delegate(tc.webhookConfig, tc.webhookErrors),
			}
			got, errs := o.ObserveAuthentication(nil, nil, baseConfig)
			if !equality.Semantic.DeepEqual(originalExisting, baseConfig) {
				t.Errorf("wrapper mutated existing config: %s", diff.Diff(originalExisting, baseConfig))
			}
			if calls != 2 {
				t.Errorf("expected both delegates to run, got %d calls", calls)
			}
			if !equality.Semantic.DeepEqual(tc.expected, got) {
				t.Errorf("unexpected config: %s", diff.Diff(tc.expected, got))
			}
			expectedErrors := append(append([]error{}, tc.oidcErrors...), tc.webhookErrors...)
			if len(errs) != len(expectedErrors) {
				t.Fatalf("expected %d errors, got %v", len(expectedErrors), errs)
			}
			for i, expected := range expectedErrors {
				if !errors.Is(errs[i], expected) {
					t.Errorf("expected error %v, got %v", expected, errs[i])
				}
			}
		})
	}
}

func TestObserveAuthenticationMissingAuthResource(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	listers := configobservation.Listers{
		AuthConfigLister: configlistersv1.NewAuthenticationLister(indexer),
	}
	calls := 0
	delegate := func(_ configobserver.Listers, _ events.Recorder, _ map[string]interface{}) (map[string]interface{}, []error) {
		calls++
		return nil, nil
	}
	o := authenticationObserver{
		featureGateAccessor:  featureGatesWithWebhook,
		externalOIDCObserver: delegate,
		webhookObserver:      delegate,
	}
	got, errs := o.ObserveAuthentication(listers, nil, baseConfig)
	if calls != 2 {
		t.Errorf("missing Authentication must fall through to both delegates; got %d calls", calls)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(got) != 0 {
		t.Errorf("expected empty delegate results to allow cleanup, got %v", got)
	}
}
