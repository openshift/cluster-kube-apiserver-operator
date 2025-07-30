package auth

import (
	"fmt"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

var (
	unprunedBaseAuthMetadataConfig = map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"authentication-token-webhook-config-file": webhookTokenAuthenticatorFile,
			"authentication-token-webhook-version":     webhookTokenAuthenticatorVersion,
		},
		"authConfig": map[string]interface{}{
			"oauthMetadataFile": "/etc/kubernetes/static-pod-resources/configmaps/oauth-metadata/oauthMetadata",
		},
	}

	prunedBaseAuthMetadataConfig = map[string]interface{}{
		"authConfig": map[string]interface{}{
			"oauthMetadataFile": "/etc/kubernetes/static-pod-resources/configmaps/oauth-metadata/oauthMetadata",
		},
	}
)

func TestObserveAuthMetadata(t *testing.T) {

	for _, tt := range []struct {
		name string

		authIndexer cache.Indexer
		cmIndexer   cache.Indexer

		existingConfig     map[string]interface{}
		authConfigMap      *corev1.ConfigMap
		authSpec           *configv1.AuthenticationSpec
		statusMetadataName string
		syncerError        error

		expectedConfig map[string]interface{}
		expectedSynced map[string]string
		expectErrors   bool
	}{
		{
			name:           "auth resource not found",
			authSpec:       nil,
			expectedConfig: map[string]interface{}{},
			expectedSynced: nil,
			expectErrors:   false,
		},
		{
			name:           "auth resource lister error",
			authSpec:       nil,
			existingConfig: unprunedBaseAuthMetadataConfig,
			authIndexer:    &everFailingIndexer{},
			expectedConfig: prunedBaseAuthMetadataConfig,
			expectedSynced: nil,
			expectErrors:   true,
		},
		{
			name:           "syncer error",
			existingConfig: unprunedBaseAuthMetadataConfig,
			syncerError:    fmt.Errorf("configmap not found"),
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "non-existing-configmap",
			expectedConfig:     prunedBaseAuthMetadataConfig,
			expectedSynced:     nil,
			expectErrors:       true,
		},
		{
			name:           "empty auth metadata without existing",
			existingConfig: nil,
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
		{
			name:           "empty auth metadata with existing",
			existingConfig: unprunedBaseAuthMetadataConfig,
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
		{
			name: "metadata from spec",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "metadata-from-spec",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     prunedBaseAuthMetadataConfig,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "configmap/metadata-from-spec.openshift-config",
			},
			expectErrors: false,
		},
		{
			name: "metadata from spec with auth type empty",
			authSpec: &configv1.AuthenticationSpec{
				Type: "",
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "metadata-from-spec",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     prunedBaseAuthMetadataConfig,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "configmap/metadata-from-spec.openshift-config",
			},
			expectErrors: false,
		},
		{
			name: "metadata from spec with auth type None",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeNone,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "metadata-from-spec",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
		{
			name: "metadata from spec with auth type OIDC but auth-config missing",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "metadata-from-spec",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced:     map[string]string{},
			expectErrors:       false,
		},
		{
			name: "metadata from spec with auth type OIDC",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "metadata-from-spec",
				},
			},
			authConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "auth-config",
					Namespace: "openshift-kube-apiserver",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
		{
			name: "metadata from status",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     prunedBaseAuthMetadataConfig,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "configmap/metadata-from-status.openshift-config-managed",
			},
			expectErrors: false,
		},
		{
			name: "metadata from status with auth type empty",
			authSpec: &configv1.AuthenticationSpec{
				Type: "",
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     prunedBaseAuthMetadataConfig,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "configmap/metadata-from-status.openshift-config-managed",
			},
			expectErrors: false,
		},
		{
			name: "metadata from status with auth type None",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeNone,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
		{
			name: "metadata from status with auth type OIDC but auth-config missing",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced:     map[string]string{},
			expectErrors:       false,
		},
		{
			name: "metadata from status with auth type OIDC",
			authSpec: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OAuthMetadata: configv1.ConfigMapNameReference{
					Name: "",
				},
			},
			authConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "auth-config",
					Namespace: "openshift-kube-apiserver",
				},
			},
			statusMetadataName: "metadata-from-status",
			expectedConfig:     nil,
			expectedSynced: map[string]string{
				"configmap/oauth-metadata.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synced := map[string]string{}
			eventRecorder := events.NewInMemoryRecorder("authmetadatatest", clock.RealClock{})

			if tt.authIndexer == nil {
				tt.authIndexer = cache.NewIndexer(func(obj interface{}) (string, error) {
					return "cluster", nil
				}, cache.Indexers{})
			}

			if tt.authSpec != nil {
				tt.authIndexer.Add(&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: *tt.authSpec,
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{
							Name: tt.statusMetadataName,
						},
					},
				})
			}

			if tt.cmIndexer == nil {
				tt.cmIndexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			}

			if tt.authConfigMap != nil {
				tt.cmIndexer.Add(tt.authConfigMap)
			}

			listers := configobservation.Listers{
				AuthConfigLister: configlistersv1.NewAuthenticationLister(tt.authIndexer),
				ConfigmapLister_: corelistersv1.NewConfigMapLister(tt.cmIndexer),
				ResourceSync:     &mockResourceSyncer{t: t, synced: synced, error: tt.syncerError},
			}

			actualConfig, errs := ObserveAuthMetadata(listers, eventRecorder, tt.existingConfig)

			if tt.expectErrors != (len(errs) > 0) {
				t.Errorf("expected errors: %v; got %v", tt.expectErrors, errs)
			}

			if !equality.Semantic.DeepEqual(tt.expectedConfig, actualConfig) {
				t.Errorf("unexpected config diff: %s", diff.Diff(tt.expectedConfig, actualConfig))
			}

			if !equality.Semantic.DeepEqual(tt.expectedSynced, synced) {
				t.Errorf("expected resources not synced: %s", diff.Diff(tt.expectedSynced, synced))
			}

		})
	}
}
