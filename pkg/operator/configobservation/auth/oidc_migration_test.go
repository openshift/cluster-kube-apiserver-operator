package auth

import (
	"strings"
	"testing"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

// TestObserveDirectOIDCToWebhookMigration checks the combined authentication
// configuration. The observer framework merges results even when an observer
// returns an error, so testing either observer alone misses loss of both paths.
// The syncer deliberately records requests without copying any resources.
func TestObserveDirectOIDCToWebhookMigration(t *testing.T) {
	valid := map[string][]byte{"kubeConfig": correctKubeConfigString}
	stale := map[string][]byte{"kubeConfig": []byte(strings.ReplaceAll(string(correctKubeConfigString), "remote-srv-address", "previous-authenticator"))}
	invalid := map[string][]byte{"kubeConfig": []byte("not a kubeconfig")}
	for name, data := range map[string]map[string][]byte{"valid": valid, "stale but valid": stale} {
		if errs := validateKubeconfigSecret(&corev1.Secret{Data: data}); len(errs) > 0 {
			t.Fatalf("%s fixture must pass kubeconfig validation: %v", name, errs)
		}
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
				name        string
				source      map[string][]byte
				destination map[string][]byte
				ready       bool
			}{
				{name: "source missing"},
				{name: "source missing kubeConfig key", source: map[string][]byte{"other": []byte("value")}},
				{name: "source kubeConfig empty", source: map[string][]byte{"kubeConfig": {}}},
				{name: "source invalid", source: invalid},
				{name: "source invalid with old valid destination", source: invalid, destination: stale},
				{name: "destination missing", source: valid},
				{name: "destination invalid", source: valid, destination: invalid},
				{name: "destination stale but valid", source: valid, destination: stale},
				{name: "destination matches source", source: valid, destination: valid, ready: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					for _, obj := range []interface{}{
						authResourceWithOIDC.DeepCopy(),
						baseSourceConfigMap.DeepCopy(),
						baseTargetConfigMap.DeepCopy(),
					} {
						if err := indexer.Add(obj); err != nil {
							t.Fatal(err)
						}
					}
					for _, secret := range []struct {
						namespace string
						name      string
						data      map[string][]byte
					}{
						{operatorclient.GlobalUserSpecifiedConfigNamespace, defaultWebhookSecretName, tc.source},
						{operatorclient.TargetNamespace, "webhook-authenticator", tc.destination},
					} {
						if secret.data == nil {
							continue
						}
						if err := indexer.Add(&corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{Namespace: secret.namespace, Name: secret.name},
							Data:       secret.data,
						}); err != nil {
							t.Fatal(err)
						}
					}

					synced := map[string]string{}
					listers := configobservation.Listers{
						AuthConfigLister:    configlistersv1.NewAuthenticationLister(indexer),
						ConfigmapLister_:    corelistersv1.NewConfigMapLister(indexer),
						ConfigSecretLister_: corelistersv1.NewSecretLister(indexer),
						SecretLister_:       corelistersv1.NewSecretLister(indexer),
						ResourceSync:        &mockResourceSyncer{t: t, synced: synced},
					}
					recorder := events.NewInMemoryRecorder("oidcmigrationtest", clock.RealClock{})
					observe := NewAuthenticationObserver(gates.access)
					existing := runtime.DeepCopyJSON(baseConfig)
					combined, errs := observe(listers, recorder, existing)
					if !equality.Semantic.DeepEqual(baseConfig, existing) {
						t.Errorf("migration mutated existing config: %s", diff.Diff(baseConfig, existing))
					}
					// Errors must not prevent checking the configuration that will be persisted.
					if len(errs) > 0 {
						t.Logf("observer errors: %v", errs)
					}
					if tc.ready && len(errs) > 0 {
						t.Errorf("synchronized kubeconfig must allow migration without errors: %v", errs)
					}

					if !tc.ready {
						if !equality.Semantic.DeepEqual(baseConfig, combined) {
							t.Errorf("pending migration must preserve direct OIDC without publishing a new webhook: %s", diff.Diff(baseConfig, combined))
						}
					} else {
						if !equality.Semantic.DeepEqual(prunedBaseWebhookAuthenticatorConfig, combined) {
							t.Errorf("synchronized kubeconfig must allow transition to webhook authentication: %s", diff.Diff(prunedBaseWebhookAuthenticatorConfig, combined))
						}
					}
					if synced["configmap/auth-config.openshift-kube-apiserver"] == "DELETE" {
						t.Error("migration observation must not request deletion of the direct OIDC ConfigMap")
					}
					direct, _, err := unstructured.NestedSlice(combined, authConfigPath...)
					if err != nil {
						t.Fatal(err)
					}
					webhook, _, err := unstructured.NestedSlice(combined, webhookTokenAuthenticatorPath...)
					if err != nil {
						t.Fatal(err)
					}
					if len(direct) == 0 && len(webhook) == 0 {
						t.Error("combined configuration has neither direct OIDC nor webhook authentication")
					}
					if tc.source != nil && len(validateKubeconfigSecret(&corev1.Secret{Data: tc.source})) == 0 {
						if got := synced["secret/webhook-authenticator.openshift-kube-apiserver"]; got != "secret/"+defaultWebhookSecretName+".openshift-config" {
							t.Errorf("valid source must request synchronization even while waiting for the destination; got %q", got)
						}
					}
				})
			}
		})
	}
}
