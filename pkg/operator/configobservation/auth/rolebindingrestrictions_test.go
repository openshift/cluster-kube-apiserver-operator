package auth

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
)

func TestObserveRoleBindingRestrictions(t *testing.T) {
	for _, tt := range []struct {
		name           string
		authType       *configv1.AuthenticationType
		existingConfig map[string]interface{}

		expectEvents   bool
		expectErrors   bool
		expectedConfig map[string]interface{}
	}{
		{
			name:           "auth resource not found",
			authType:       nil,
			existingConfig: map[string]interface{}{"key": "value"},
			expectEvents:   true,
			expectErrors:   false,
			expectedConfig: nil,
		},
		{
			name:           "auth type IntegratedOAuth without other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeIntegratedOAuth),
			existingConfig: nil,
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: nil,
		},
		{
			name:           "auth type empty without other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationType("")),
			existingConfig: nil,
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: nil,
		},
		{
			name:           "auth type OIDC without other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeOIDC),
			existingConfig: nil,
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{rbrPlugins[0], rbrPlugins[1]}),
		},
		{
			name:           "auth type None without other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeNone),
			existingConfig: nil,
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{rbrPlugins[0], rbrPlugins[1]}),
		},
		{
			name:           "auth type IntegratedOAuth with other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeIntegratedOAuth),
			existingConfig: newTestConfig([]string{"off1", "off2"}),
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{"off1", "off2"}),
		},
		{
			name:           "auth type empty with other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationType("")),
			existingConfig: newTestConfig([]string{"off1", "off2"}),
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{"off1", "off2"}),
		},
		{
			name:           "auth type OIDC with other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeOIDC),
			existingConfig: newTestConfig([]string{"off1", "off2"}),
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{rbrPlugins[0], rbrPlugins[1], "off1", "off2"}),
		},
		{
			name:           "auth type None with other disabled plugins in config",
			authType:       ptr.To(configv1.AuthenticationTypeNone),
			existingConfig: newTestConfig([]string{"off1", "off2"}),
			expectEvents:   false,
			expectErrors:   false,
			expectedConfig: newTestConfig([]string{rbrPlugins[0], rbrPlugins[1], "off1", "off2"}),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if tt.authType != nil {
				indexer.Add(&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cluster",
					},
					Spec: configv1.AuthenticationSpec{
						Type: *tt.authType,
					},
				})
			}

			eventRecorder := events.NewInMemoryRecorder("externaloidctest", clock.RealClock{})
			listers := configobservation.Listers{
				AuthConfigLister: configlistersv1.NewAuthenticationLister(indexer),
			}

			actualConfig, actualErrs := ObserveRoleBindingRestrictionPlugins(listers, eventRecorder, tt.existingConfig)
			if tt.expectErrors != (len(actualErrs) > 0) {
				t.Errorf("expected errors: %v; got %v", tt.expectErrors, actualErrs)
			}

			if !equality.Semantic.DeepEqual(tt.expectedConfig, actualConfig) {
				t.Errorf("unexpected config diff: %s", diff.ObjectReflectDiff(tt.expectedConfig, actualConfig))
			}

			if recordedEvents := eventRecorder.Events(); tt.expectEvents != (len(recordedEvents) > 0) {
				t.Errorf("expected events: %v; got %v", tt.expectEvents, recordedEvents)
			}
		})
	}
}

func newTestConfig(disabled []string) map[string]interface{} {
	cfg := map[string]interface{}{}

	if len(disabled) > 0 {
		if err := unstructured.SetNestedStringSlice(cfg, disabled, "apiServerArguments", "disable-admission-plugins"); err != nil {
			panic(err)
		}
	}

	return cfg
}
