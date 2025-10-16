package auth

import (
	"fmt"
	"path"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

var (
	featureGatesWithOIDC = featuregates.NewHardcodedFeatureGateAccessForTesting(
		[]configv1.FeatureGateName{features.FeatureGateExternalOIDC},
		[]configv1.FeatureGateName{},
		makeClosedChannel(),
		nil,
	)

	authResourceWithOAuth = configv1.Authentication{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeIntegratedOAuth,
		},
	}

	authResourceWithOIDC = configv1.Authentication{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
		},
	}

	baseConfig = map[string]interface{}{
		"apiServerArguments": map[string]interface{}{
			"authentication-config": []interface{}{path.Join("/etc/kubernetes/static-pod-resources/configmaps/", AuthConfigCMName, authConfigKeyName)},
		},
	}

	baseSourceConfigMap = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
			Namespace: "openshift-config-managed",
		},
		Data: map[string]string{
			"auth-config.json": `{"kind":"AuthenticationConfiguration","apiVersion":"apiserver.config.k8s.io/v1beta1"}`,
		},
	}

	invalidSourceConfigMap = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
			Namespace: "openshift-config-managed",
		},
		Data: map[string]string{
			"invalid-auth-config.json": `{}`,
		},
	}

	emptyValueSourceConfigMap = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
			Namespace: "openshift-config-managed",
		},
		Data: map[string]string{
			"auth-config.json": ``,
		},
	}

	updatedBaseSourceConfigMap = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
			Namespace: "openshift-config-managed",
		},
		Data: map[string]string{
			"auth-config.json": `{"kind":"AuthenticationConfiguration","apiVersion":"apiserver.config.k8s.io/v1beta1","jwt":[]}`,
		},
	}

	baseTargetConfigMap = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
			Namespace: "openshift-kube-apiserver",
		},
		Data: map[string]string{
			"auth-config.json": `{"kind":"AuthenticationConfiguration","apiVersion":"apiserver.config.k8s.io/v1beta1"}`,
		},
	}
)

func TestObserveExternalOIDC(t *testing.T) {
	for _, tt := range []struct {
		name string

		featureGates   featuregates.FeatureGateAccess
		existingConfig map[string]interface{}

		existingSourceConfigMap *corev1.ConfigMap
		existingTargetConfigMap *corev1.ConfigMap

		auth             *configv1.Authentication
		authIndexer      cache.Indexer
		cmIndexer        cache.Indexer
		listerErrorForNS sets.Set[string]
		syncerError      error

		expectedConfig map[string]interface{}
		expectedSynced map[string]string
		expectErrors   bool
		expectEvents   bool
	}{
		{
			name: "initial feature gates not observed",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{},
				make(chan struct{}),
				nil,
			),
			existingConfig: baseConfig,
			expectedConfig: baseConfig,
			expectErrors:   false,
		},
		{
			name: "feature gates access error",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{},
				makeClosedChannel(),
				fmt.Errorf("error"),
			),
			existingConfig: baseConfig,
			expectedConfig: baseConfig,
			expectErrors:   true,
		},
		{
			name: "ExternalOIDC feature gate disabled",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{features.FeatureGateExternalOIDC},
				makeClosedChannel(),
				nil,
			),
			existingConfig: baseConfig,
			expectedConfig: baseConfig,
			expectErrors:   false,
		},
		{
			name: "ExternalOIDC feature gate disabled with oauth config",
			featureGates: featuregates.NewHardcodedFeatureGateAccessForTesting(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{features.FeatureGateExternalOIDC},
				makeClosedChannel(),
				nil,
			),
			existingConfig: map[string]interface{}{
				"apiServerArguments": map[string]interface{}{
					"authentication-token-webhook-config-file": []interface{}{webhookTokenAuthenticatorFile},
				},
			},
			expectedConfig: nil,
			expectErrors:   false,
		},
		{
			name:           "auth resource not found",
			featureGates:   featureGatesWithOIDC,
			existingConfig: baseConfig,
			expectedConfig: baseConfig,
			expectErrors:   false,
			expectEvents:   true,
		},
		{
			name:           "auth resource retrieval error",
			featureGates:   featureGatesWithOIDC,
			authIndexer:    &everFailingIndexer{},
			existingConfig: baseConfig,
			expectedConfig: baseConfig,
			expectErrors:   true,
		},
		{
			name:                    "OAuth target configmap does not exist",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          nil,
			existingTargetConfigMap: nil,
			auth:                    &authResourceWithOAuth,
			expectedConfig:          nil,
			expectedSynced: map[string]string{
				"configmap/auth-config.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
			expectEvents: false,
		},
		{
			name:                    "OAuth target configmap syncer error",
			featureGates:            featureGatesWithOIDC,
			syncerError:             fmt.Errorf("syncer error"),
			existingConfig:          nil,
			existingTargetConfigMap: &baseTargetConfigMap,
			auth:                    &authResourceWithOAuth,
			expectedConfig:          nil,
			expectedSynced:          nil,
			expectErrors:            true,
			expectEvents:            false,
		},
		{
			name:                    "OAuth target configmap exists",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          nil,
			existingTargetConfigMap: &baseTargetConfigMap,
			auth:                    &authResourceWithOAuth,
			expectedConfig:          nil,
			expectedSynced: map[string]string{
				"configmap/auth-config.openshift-kube-apiserver": "DELETE",
			},
			expectErrors: false,
			expectEvents: true,
		},
		{
			name:                    "OIDC new invalid config with expected key missing",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          nil,
			existingSourceConfigMap: &invalidSourceConfigMap,
			auth:                    &authResourceWithOIDC,
			expectEvents:            false,
			expectErrors:            true,
		},
		{
			name:                    "OIDC updated invalid config",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          baseConfig,
			existingSourceConfigMap: &invalidSourceConfigMap,
			auth:                    &authResourceWithOIDC,
			expectedConfig:          baseConfig,
			expectEvents:            false,
			expectErrors:            true,
		},
		{
			name:                    "OIDC new valid config syncer error",
			featureGates:            featureGatesWithOIDC,
			syncerError:             fmt.Errorf("syncer error"),
			existingConfig:          nil,
			existingSourceConfigMap: &baseSourceConfigMap,
			auth:                    &authResourceWithOIDC,
			expectedConfig:          nil,
			expectedSynced:          nil,
			expectEvents:            false,
			expectErrors:            true,
		},
		{
			name:                    "OIDC new valid config",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          nil,
			existingSourceConfigMap: &baseSourceConfigMap,
			existingTargetConfigMap: nil,
			auth:                    &authResourceWithOIDC,
			expectedConfig:          baseConfig,
			expectedSynced: map[string]string{
				"configmap/auth-config.openshift-kube-apiserver": "configmap/auth-config.openshift-config-managed",
			},
			expectEvents: true,
			expectErrors: false,
		},
		{
			name:                    "OIDC updated valid config without changes",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          baseConfig,
			existingSourceConfigMap: &baseSourceConfigMap,
			existingTargetConfigMap: &baseTargetConfigMap,
			auth:                    &authResourceWithOIDC,
			expectedConfig:          baseConfig,
			expectedSynced: map[string]string{
				"configmap/auth-config.openshift-kube-apiserver": "configmap/auth-config.openshift-config-managed",
			},
			expectEvents: false,
			expectErrors: false,
		},
		{
			name:                    "OIDC updated valid config with changes",
			featureGates:            featureGatesWithOIDC,
			existingConfig:          baseConfig,
			existingSourceConfigMap: &updatedBaseSourceConfigMap,
			existingTargetConfigMap: &baseTargetConfigMap,
			auth:                    &authResourceWithOIDC,
			expectedConfig:          baseConfig,
			expectedSynced: map[string]string{
				"configmap/auth-config.openshift-kube-apiserver": "configmap/auth-config.openshift-config-managed",
			},
			expectEvents: false,
			expectErrors: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synced := map[string]string{}
			eventRecorder := events.NewInMemoryRecorder("externaloidctest", clock.RealClock{})

			if tt.authIndexer == nil {
				tt.authIndexer = cache.NewIndexer(func(obj interface{}) (string, error) {
					return "cluster", nil
				}, cache.Indexers{})
			}

			if tt.auth != nil {
				tt.authIndexer.Add(tt.auth)
			}

			if tt.cmIndexer == nil {
				tt.cmIndexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			}

			if tt.existingSourceConfigMap != nil {
				tt.cmIndexer.Add(tt.existingSourceConfigMap)
			}

			if tt.existingTargetConfigMap != nil {
				tt.cmIndexer.Add(tt.existingTargetConfigMap)
			}

			listers := configobservation.Listers{
				AuthConfigLister: configlistersv1.NewAuthenticationLister(tt.authIndexer),
				ConfigmapLister_: newFakeConfigMapLister(tt.listerErrorForNS, tt.cmIndexer),
				ResourceSync:     &mockResourceSyncer{t: t, synced: synced, error: tt.syncerError},
			}

			c := externalOIDC{featureGateAccessor: tt.featureGates}
			actualConfig, errs := c.ObserveExternalOIDC(listers, eventRecorder, tt.existingConfig)

			if tt.expectErrors != (len(errs) > 0) {
				t.Errorf("expected errors: %v; got %v", tt.expectErrors, errs)
			}

			if recordedEvents := eventRecorder.Events(); tt.expectEvents != (len(recordedEvents) > 0) {
				t.Errorf("expected events: %v; got %v", tt.expectEvents, recordedEvents)
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

func TestValidateSourceConfigMap(t *testing.T) {

	for _, tt := range []struct {
		name              string
		cmIndexer         cache.Indexer
		expectedConfigMap *corev1.ConfigMap
		expectError       bool
	}{
		{
			name:              "source configmap not found",
			cmIndexer:         cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}),
			expectedConfigMap: nil,
			expectError:       false,
		},
		{
			name:              "configmap lister error",
			cmIndexer:         &everFailingIndexer{},
			expectedConfigMap: nil,
			expectError:       true,
		},
		{
			name: "required key missing",
			cmIndexer: func() cache.Indexer {
				indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
				indexer.Add(&invalidSourceConfigMap)
				return indexer
			}(),
			expectedConfigMap: nil,
			expectError:       true,
		},
		{
			name: "required key has empty value",
			cmIndexer: func() cache.Indexer {
				indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
				indexer.Add(&emptyValueSourceConfigMap)
				return indexer
			}(),
			expectedConfigMap: nil,
			expectError:       true,
		},
		{
			name: "source configmap valid",
			cmIndexer: func() cache.Indexer {
				indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
				indexer.Add(&baseSourceConfigMap)
				return indexer
			}(),
			expectedConfigMap: &baseSourceConfigMap,
			expectError:       false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listers := configobservation.Listers{
				ConfigmapLister_: corelistersv1.NewConfigMapLister(tt.cmIndexer),
			}

			cm, err := validateSourceConfigMap(listers)

			if tt.expectError != (err != nil) {
				t.Errorf("expected error: %v; got: %v", tt.expectError, err)
			}

			if !equality.Semantic.DeepEqual(tt.expectedConfigMap, cm) {
				t.Errorf("unexpected config map: %s", diff.Diff(tt.expectedConfigMap, cm))
			}

		})
	}
}

func makeClosedChannel() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

type fakeConfigMapLister struct {
	failingNamespaces sets.Set[string]
	defaultLister     corelistersv1.ConfigMapLister
}

func newFakeConfigMapLister(failingNamespaces sets.Set[string], defaultIndexer cache.Indexer) *fakeConfigMapLister {
	return &fakeConfigMapLister{
		failingNamespaces: failingNamespaces,
		defaultLister:     corelistersv1.NewConfigMapLister(defaultIndexer),
	}
}

func (l *fakeConfigMapLister) List(selector labels.Selector) (ret []*corev1.ConfigMap, err error) {
	return l.defaultLister.List(selector)
}

func (l *fakeConfigMapLister) ConfigMaps(namespace string) corelistersv1.ConfigMapNamespaceLister {
	if l.failingNamespaces.Has(namespace) {
		return corelistersv1.NewConfigMapLister(&everFailingIndexer{}).ConfigMaps(namespace)
	}

	return l.defaultLister.ConfigMaps(namespace)
}

type everFailingIndexer struct{}

// Index always returns an error
func (i *everFailingIndexer) Index(indexName string, obj interface{}) ([]interface{}, error) {
	return nil, fmt.Errorf("Index method not implemented")
}

// IndexKeys always returns an error
func (i *everFailingIndexer) IndexKeys(indexName, indexedValue string) ([]string, error) {
	return nil, fmt.Errorf("IndexKeys method not implemented")
}

// ListIndexFuncValues always returns an error
func (i *everFailingIndexer) ListIndexFuncValues(indexName string) []string {
	return nil
}

// ByIndex always returns an error
func (i *everFailingIndexer) ByIndex(indexName, indexedValue string) ([]interface{}, error) {
	return nil, fmt.Errorf("ByIndex method not implemented")
}

// GetIndexers always returns an error
func (i *everFailingIndexer) GetIndexers() cache.Indexers {
	return nil
}

// AddIndexers always returns an error
func (i *everFailingIndexer) AddIndexers(newIndexers cache.Indexers) error {
	return fmt.Errorf("AddIndexers method not implemented")
}

// Add always returns an error
func (s *everFailingIndexer) Add(obj interface{}) error {
	return fmt.Errorf("Add method not implemented")
}

// Update always returns an error
func (s *everFailingIndexer) Update(obj interface{}) error {
	return fmt.Errorf("Update method not implemented")
}

// Delete always returns an error
func (s *everFailingIndexer) Delete(obj interface{}) error {
	return fmt.Errorf("Delete method not implemented")
}

// List always returns nil
func (s *everFailingIndexer) List() []interface{} {
	return nil
}

// ListKeys always returns nil
func (s *everFailingIndexer) ListKeys() []string {
	return nil
}

// Get always returns an error
func (s *everFailingIndexer) Get(obj interface{}) (item interface{}, exists bool, err error) {
	return nil, false, fmt.Errorf("Get method not implemented")
}

// GetByKey always returns an error
func (s *everFailingIndexer) GetByKey(key string) (item interface{}, exists bool, err error) {
	return nil, false, fmt.Errorf("GetByKey method not implemented")
}

// Replace always returns an error
func (s *everFailingIndexer) Replace(objects []interface{}, sKey string) error {
	return fmt.Errorf("Replace method not implemented")
}

// Resync always returns an error
func (s *everFailingIndexer) Resync() error {
	return fmt.Errorf("Resync method not implemented")
}
