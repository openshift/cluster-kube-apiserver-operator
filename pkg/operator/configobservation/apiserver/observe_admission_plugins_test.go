package apiserver

import (
	"fmt"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
)

func TestObserveAdmissionPlugins(t *testing.T) {
	for _, tt := range []struct {
		name           string
		pluginCheckers []pluginCheckerFunc
		existingConfig map[string]any

		expectErrors   bool
		expectedConfig map[string]any
	}{
		{
			name:           "no plugin checkers available",
			existingConfig: map[string]any{"key": "value"},
			pluginCheckers: []pluginCheckerFunc{},
			expectErrors:   false,
			expectedConfig: map[string]any{},
		},
		{
			name: "observer returns pruned config",
			existingConfig: map[string]any{
				"key": "value",
				"apiServerArguments": map[string]any{
					"enable-admission-plugins":  []any{"enabled1", "enabled2"},
					"disable-admission-plugins": []any{"disabled1", "disabled2"},
				},
			},
			pluginCheckers: []pluginCheckerFunc{},
			expectErrors:   false,
			expectedConfig: map[string]any{
				"apiServerArguments": map[string]any{
					"enable-admission-plugins":  []any{"enabled1", "enabled2"},
					"disable-admission-plugins": []any{"disabled1", "disabled2"},
				},
			},
		},
		{
			name:           "plugin checker with error",
			existingConfig: map[string]any{"key": "value"},
			pluginCheckers: []pluginCheckerFunc{
				func(_ configobservation.Listers) ([]string, []string, error) {
					return nil, nil, fmt.Errorf("plugin checker error")
				},
			},
			expectErrors:   true,
			expectedConfig: map[string]any{},
		},
		{
			name:           "plugin checkers must enable and disable plugins",
			existingConfig: map[string]any{"key": "value"},
			pluginCheckers: []pluginCheckerFunc{
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled1"}, nil, nil
				},
				func(_ configobservation.Listers) ([]string, []string, error) {
					return nil, []string{"disabled1"}, nil
				},
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled2"}, []string{"disabled2"}, nil
				},
			},
			expectErrors: false,
			expectedConfig: map[string]any{
				"apiServerArguments": map[string]any{
					"enable-admission-plugins":  []any{"enabled1", "enabled2"},
					"disable-admission-plugins": []any{"disabled1", "disabled2"},
				},
			},
		},
		{
			name: "plugin checkers must overwrite existing enabled and disabled",
			existingConfig: map[string]any{
				"key": "value",
				"apiServerArguments": map[string]any{
					"enable-admission-plugins":  []any{"another1"},
					"disable-admission-plugins": []any{"another2"},
				},
			},
			pluginCheckers: []pluginCheckerFunc{
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled1"}, nil, nil
				},
				func(_ configobservation.Listers) ([]string, []string, error) {
					return nil, []string{"disabled1"}, nil
				},
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled2"}, []string{"disabled2"}, nil
				},
			},
			expectErrors: false,
			expectedConfig: map[string]any{
				"apiServerArguments": map[string]any{
					"enable-admission-plugins":  []any{"enabled1", "enabled2"},
					"disable-admission-plugins": []any{"disabled1", "disabled2"},
				},
			},
		},
		{
			name: "plugin checkers must return disjoint enabled and disabled plugin slices",
			pluginCheckers: []pluginCheckerFunc{
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled1", "enabled2"}, nil, nil
				},
				func(_ configobservation.Listers) ([]string, []string, error) {
					return []string{"enabled3"}, []string{"enabled2"}, nil
				},
			},
			expectErrors: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pluginCheckers = tt.pluginCheckers

			eventRecorder := events.NewInMemoryRecorder("TestObserveAdmissionPlugins", clocktesting.NewFakePassiveClock(time.Now()))
			listers := configobservation.Listers{}
			gotConfig, gotErrs := ObserveAdmissionPlugins(listers, eventRecorder, tt.existingConfig)

			if tt.expectErrors != (len(gotErrs) > 0) {
				t.Errorf("expected errors: %v; got %v", tt.expectErrors, gotErrs)
			}

			if !equality.Semantic.DeepEqual(tt.expectedConfig, gotConfig) {
				t.Errorf("unexpected config diff: %s", diff.Diff(tt.expectedConfig, gotConfig))
			}
		})
	}
}

func TestRoleBindingRestrictionPluginChecker(t *testing.T) {
	for _, tt := range []struct {
		name             string
		authType         *configv1.AuthenticationType
		expectedEnabled  []string
		expectedDisabled []string
		expectError      bool
	}{
		{
			name:        "authentication cluster not found",
			expectError: true,
		},
		{
			name:     "auth type IntegratedOAuth",
			authType: ptr.To(configv1.AuthenticationTypeIntegratedOAuth),
			expectedEnabled: []string{
				"authorization.openshift.io/RestrictSubjectBindings",
				"authorization.openshift.io/ValidateRoleBindingRestriction",
			},
			expectedDisabled: []string{},
			expectError:      false,
		},
		{
			name:     "auth type empty string",
			authType: ptr.To(configv1.AuthenticationType("")),
			expectedEnabled: []string{
				"authorization.openshift.io/RestrictSubjectBindings",
				"authorization.openshift.io/ValidateRoleBindingRestriction",
			},
			expectedDisabled: []string{},
			expectError:      false,
		},
		{
			name:            "auth type None",
			authType:        ptr.To(configv1.AuthenticationTypeNone),
			expectedEnabled: []string{},
			expectedDisabled: []string{
				"authorization.openshift.io/RestrictSubjectBindings",
				"authorization.openshift.io/ValidateRoleBindingRestriction",
			},
			expectError: false,
		},
		{
			name:            "auth type OIDC",
			authType:        ptr.To(configv1.AuthenticationTypeOIDC),
			expectedEnabled: []string{},
			expectedDisabled: []string{
				"authorization.openshift.io/RestrictSubjectBindings",
				"authorization.openshift.io/ValidateRoleBindingRestriction",
			},
			expectError: false,
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

			listers := configobservation.Listers{
				AuthConfigLister: configlistersv1.NewAuthenticationLister(indexer),
			}

			enabled, disabled, err := roleBindingRestrictionPluginChecker(listers)
			if tt.expectError != (err != nil) {
				t.Errorf("expected errors: %v; got %v", tt.expectError, err)
			}

			if !equality.Semantic.DeepEqual(tt.expectedEnabled, enabled) {
				t.Errorf("unexpected enabled plugins: %s", diff.Diff(tt.expectedEnabled, enabled))
			}

			if !equality.Semantic.DeepEqual(tt.expectedDisabled, disabled) {
				t.Errorf("unexpected disabled plugins: %s", diff.Diff(tt.expectedDisabled, disabled))
			}
		})
	}
}
