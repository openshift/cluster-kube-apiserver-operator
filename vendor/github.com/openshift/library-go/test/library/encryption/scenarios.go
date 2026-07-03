package encryption

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
)

type BasicScenario struct {
	Namespace                       string
	LabelSelector                   string
	EncryptionConfigSecretName      string
	EncryptionConfigSecretNamespace string
	OperatorNamespace               string
	TargetGRs                       []schema.GroupResource
	AssertFunc                      func(t testing.TB, clientSet ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string)
}

// EncryptionProvider pairs an encryption config with an optional setup function
// that ensures prerequisites (secrets, credentials, infrastructure) are in place.
type EncryptionProvider struct {
	configv1.APIServerEncryption
	// Setup is called once before the provider is first used. May be nil.
	Setup func(ctx context.Context, t testing.TB)
}

func TestEncryptionTypeIdentity(ctx context.Context, t testing.TB, scenario BasicScenario) {
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := SetAndWaitForEncryptionType(ctx, e, EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: configv1.EncryptionTypeIdentity}}, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(e, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
}

func TestEncryptionTypeUnset(ctx context.Context, t testing.TB, scenario BasicScenario) {
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := SetAndWaitForEncryptionType(ctx, e, EncryptionProvider{}, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(e, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
}

func resolveProvider(t testing.TB, defaultType configv1.EncryptionType, providers []EncryptionProvider) EncryptionProvider {
	t.Helper()
	if len(providers) > 1 {
		t.Fatalf("expected at most one provider, got %d", len(providers))
	}
	if len(providers) == 1 {
		return providers[0]
	}
	return EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: defaultType}}
}

func TestEncryptionTypeAESCBC(ctx context.Context, t testing.TB, scenario BasicScenario, providers ...EncryptionProvider) {
	provider := resolveProvider(t, configv1.EncryptionTypeAESCBC, providers)
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := SetAndWaitForEncryptionType(ctx, e, provider, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(e, clientSet, provider.Type, scenario.Namespace, scenario.LabelSelector)
	AssertEncryptionConfig(e, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
}

func TestEncryptionTypeAESGCM(ctx context.Context, t testing.TB, scenario BasicScenario, providers ...EncryptionProvider) {
	provider := resolveProvider(t, configv1.EncryptionTypeAESGCM, providers)
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := SetAndWaitForEncryptionType(ctx, e, provider, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(e, clientSet, provider.Type, scenario.Namespace, scenario.LabelSelector)
	AssertEncryptionConfig(e, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
}

func TestEncryptionTypeKMS(ctx context.Context, t testing.TB, scenario BasicScenario, providers ...EncryptionProvider) {
	provider := resolveProvider(t, configv1.EncryptionTypeKMS, providers)
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := SetAndWaitForEncryptionType(ctx, e, provider, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
	scenario.AssertFunc(e, clientSet, provider.Type, scenario.Namespace, scenario.LabelSelector)
	AssertEncryptionConfig(e, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
}

func TestEncryptionType(ctx context.Context, t testing.TB, scenario BasicScenario, provider EncryptionProvider) {
	switch provider.Type {
	case configv1.EncryptionTypeAESCBC:
		TestEncryptionTypeAESCBC(ctx, t, scenario, provider)
	case configv1.EncryptionTypeAESGCM:
		TestEncryptionTypeAESGCM(ctx, t, scenario, provider)
	case configv1.EncryptionTypeKMS:
		TestEncryptionTypeKMS(ctx, t, scenario, provider)
	case configv1.EncryptionTypeIdentity, "":
		TestEncryptionTypeIdentity(ctx, t, scenario)
	default:
		t.Fatalf("Unknown encryption type: %s", provider.Type)
	}
}

type OnOffScenario struct {
	BasicScenario
	CreateResourceFunc             func(t testing.TB, clientSet ClientSet, namespace string) runtime.Object
	AssertResourceEncryptedFunc    func(t testing.TB, clientSet ClientSet, resource runtime.Object)
	AssertResourceNotEncryptedFunc func(t testing.TB, clientSet ClientSet, resource runtime.Object)
	ResourceFunc                   func(t testing.TB, namespace string) runtime.Object
	ResourceName                   string
	EncryptionProvider             EncryptionProvider
}

type testStep struct {
	name     string
	testFunc func(testing.TB)
}

// TestEncryptionTurnOnAndOff tests encryption on/off cycles across one or more
// operator scenarios. All scenarios must share the same EncryptionProvider since
// the encryption config is a single global resource.
//
// The test:
//  1. Creates test resources for all scenarios
//  2. Enables encryption, waits for each operator's key migration, asserts encrypted
//  3. Disables (identity), waits for each operator, asserts not encrypted
//  4. Repeats steps 2-3 a second time
func TestEncryptionTurnOnAndOff(ctx context.Context, t testing.TB, scenarios ...OnOffScenario) {
	if len(scenarios) == 0 {
		t.Fatalf("TestEncryptionTurnOnAndOff requires at least one scenario")
	}

	provider := scenarios[0].EncryptionProvider

	// step 1: create test resources for all scenarios
	var steps []testStep
	for _, s := range scenarios {
		steps = append(steps, testStep{name: fmt.Sprintf("CreateAndStore%s", s.ResourceName), testFunc: func(t testing.TB) {
			e := NewE(t)
			s.CreateResourceFunc(e, GetClients(e), s.Namespace)
		}})
	}

	// step 2-3: two on/off cycles
	for cycle := 1; cycle <= 2; cycle++ {
		suffix := ""
		if cycle == 2 {
			suffix = "Second"
		}

		// enable encryption and wait for each operator
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("On%s%s[%s]", strings.ToUpper(string(provider.Type)), suffix, s.ResourceName), testFunc: func(t testing.TB) {
				TestEncryptionType(ctx, t, s.BasicScenario, provider)
			}})
		}
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("Assert%sEncrypted%s", s.ResourceName, suffix), testFunc: func(t testing.TB) {
				e := NewE(t)
				s.AssertResourceEncryptedFunc(e, GetClients(e), s.ResourceFunc(e, s.Namespace))
			}})
		}

		// disable encryption (identity) and wait for each operator
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("OffIdentity%s[%s]", suffix, s.ResourceName), testFunc: func(t testing.TB) {
				TestEncryptionTypeIdentity(ctx, t, s.BasicScenario)
			}})
		}
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("Assert%sNotEncrypted%s", s.ResourceName, suffix), testFunc: func(t testing.TB) {
				e := NewE(t)
				s.AssertResourceNotEncryptedFunc(e, GetClients(e), s.ResourceFunc(e, s.Namespace))
			}})
		}
	}

	// run steps
	for _, step := range steps {
		t.Logf("=== STEP: %s ===", step.name)
		step.testFunc(t)
		if t.Failed() {
			t.Errorf("stopping the test as %q step failed", step.name)
			return
		}
	}
}

// ProvidersMigrationScenario defines a test scenario for migrating encryption
// between multiple providers.
//
// See TestEncryptionProvidersMigration for more details.
type ProvidersMigrationScenario struct {
	BasicScenario
	CreateResourceFunc             func(t testing.TB, clientSet ClientSet, namespace string) runtime.Object
	AssertResourceEncryptedFunc    func(t testing.TB, clientSet ClientSet, resource runtime.Object)
	AssertResourceNotEncryptedFunc func(t testing.TB, clientSet ClientSet, resource runtime.Object)
	ResourceFunc                   func(t testing.TB, namespace string) runtime.Object
	ResourceName                   string
	// EncryptionProviders is the list of encryption providers to migrate through.
	// The test will migrate through each provider in order, then always end by
	// switching to identity (off) to verify the resource is re-written unencrypted.
	EncryptionProviders []EncryptionProvider
}

// ShuffleEncryptionProviders returns a new slice with the providers in random order,
// leaving the original slice unchanged. Use this to test different migration orderings.
func ShuffleEncryptionProviders(providers []EncryptionProvider) []EncryptionProvider {
	shuffled := make([]EncryptionProvider, len(providers))
	copy(shuffled, providers)
	mathrand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return shuffled
}

// TestEncryptionProvidersMigration tests migration between given encryption providers
// across one or more operator scenarios. All scenarios must share the same
// EncryptionProviders list since the encryption config is a single global resource.
//
// For each provider in the list, the test:
//  1. Creates test resources for all scenarios
//  2. Sets the encryption type and waits for each operator's key migration
//  3. Asserts each operator's test resource is encrypted
//  4. Finally switches to identity (off) and verifies all resources are decrypted
func TestEncryptionProvidersMigration(ctx context.Context, t testing.TB, scenarios ...ProvidersMigrationScenario) {
	if len(scenarios) == 0 {
		t.Fatalf("TestEncryptionProvidersMigration requires at least one scenario")
	}

	providers := scenarios[0].EncryptionProviders
	if len(providers) < 2 {
		t.Fatalf("ProvidersMigrationScenario requires at least 2 encryption providers, got %d", len(providers))
	}
	for _, provider := range providers {
		if provider.Type == configv1.EncryptionTypeIdentity || provider.Type == "" {
			t.Fatalf("Unsupported encryption provider %q passed", provider.Type)
		}
	}

	// step 1: create test resources for all scenarios
	var steps []testStep
	for _, s := range scenarios {
		steps = append(steps, testStep{name: fmt.Sprintf("CreateAndStore%s", s.ResourceName), testFunc: func(t testing.TB) {
			e := NewE(t)
			s.CreateResourceFunc(e, GetClients(e), s.Namespace)
		}})
	}

	// step 2: migrate through each provider in sequence
	for i, provider := range providers {
		prefix := "EncryptWith"
		if i > 0 {
			prefix = "MigrateTo"
		}

		// wait for each operator's key migration
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("%s%s[%s]", prefix, strings.ToUpper(string(provider.Type)), s.ResourceName), testFunc: func(t testing.TB) {
				TestEncryptionType(ctx, t, s.BasicScenario, provider)
			}})
		}

		// assert each resource is encrypted
		for _, s := range scenarios {
			steps = append(steps, testStep{name: fmt.Sprintf("Assert%sEncrypted", s.ResourceName), testFunc: func(t testing.TB) {
				e := NewE(t)
				s.AssertResourceEncryptedFunc(e, GetClients(e), s.ResourceFunc(e, s.Namespace))
			}})
		}
	}

	// step 3: switch to identity (off) and verify each resource is decrypted
	for _, s := range scenarios {
		steps = append(steps, testStep{name: fmt.Sprintf("OffIdentityAndAssert%sNotEncrypted", s.ResourceName), testFunc: func(t testing.TB) {
			TestEncryptionTypeIdentity(ctx, t, s.BasicScenario)
			e := NewE(t)
			s.AssertResourceNotEncryptedFunc(e, GetClients(e), s.ResourceFunc(e, s.Namespace))
		}})
	}

	// run steps
	for _, step := range steps {
		t.Logf("=== STEP: %s ===", step.name)
		step.testFunc(t)
		if t.Failed() {
			t.Errorf("stopping the test as %q step failed", step.name)
			return
		}
	}
}

type RotationScenario struct {
	BasicScenario
	CreateResourceFunc          func(t testing.TB, clientSet ClientSet, namespace string) runtime.Object
	GetRawResourceFunc          func(t testing.TB, clientSet ClientSet, namespace string) string
	EncryptionProvider          EncryptionProvider
	ForceRotationFunc           ForceRotationFunc
	WaitForRotationCompleteFunc WaitForRotationCompleteFunc
}

// TestEncryptionRotation encrypts data, forces a provider-specific key rotation, waits for
// re-migration to complete, and verifies the resource was re-encrypted with different content.
func TestEncryptionRotation(ctx context.Context, t testing.TB, scenario RotationScenario) {
	// test data
	ns := scenario.Namespace
	labelSelector := scenario.LabelSelector

	// step 1: create the desired resource
	e := NewE(t)
	clientSet := GetClients(e)
	scenario.CreateResourceFunc(e, clientSet, ns)

	// step 2: run provided encryption scenario
	TestEncryptionType(ctx, t, scenario.BasicScenario, scenario.EncryptionProvider)

	// step 3: take samples
	rawEncryptedResourceWithKey1 := scenario.GetRawResourceFunc(e, clientSet, ns)

	// step 4: force key rotation and wait for migration to complete
	lastMigratedKeyMeta, err := GetLastKeyMeta(t, clientSet.Kube, ns, labelSelector)
	require.NoError(e, err)
	t.Logf("Forcing key rotation for %q encryption", scenario.EncryptionProvider.Type)
	scenario.ForceRotationFunc(e, ctx)

	t.Logf("Waiting for rotation migration to complete")
	scenario.WaitForRotationCompleteFunc(e, clientSet, lastMigratedKeyMeta, scenario.BasicScenario)

	scenario.AssertFunc(e, clientSet, scenario.EncryptionProvider.Type, ns, labelSelector)

	// step 5: verify if the provided resource was encrypted with a different key (step 2 vs step 4)
	rawEncryptedResourceWithKey2 := scenario.GetRawResourceFunc(e, clientSet, ns)
	if rawEncryptedResourceWithKey1 == rawEncryptedResourceWithKey2 {
		t.Errorf("expected the resource to have different content after a key rotation,\ncontentBeforeRotation %s\ncontentAfterRotation %s", rawEncryptedResourceWithKey1, rawEncryptedResourceWithKey2)
	}

	// TODO: assert conditions - operator and encryption migration controller must report status as active not progressing, and not failing for all scenarios
}

// ApplyEncryption applies the given encryption config to apiserver/cluster
// without waiting for completion.
func ApplyEncryption(ctx context.Context, t testing.TB, encryption configv1.APIServerEncryption) {
	t.Helper()
	cs := GetClients(t)
	apiServer, err := cs.ApiServerConfig.Get(ctx, "cluster", metav1.GetOptions{})
	require.NoError(t, err)
	apiServer.Spec.Encryption = encryption
	_, err = cs.ApiServerConfig.Update(ctx, apiServer, metav1.UpdateOptions{})
	require.NoError(t, err)
	t.Logf("Applied encryption config (type=%s)", encryption.Type)
}

// KMSInPlaceUpdateScenario tests that updating an in-place KMS config field
// (e.g. kmsPluginImage) takes effect without creating a new encryption key.
// The caller supplies Provider (initial valid config) and UpdatedProvider (same config
// with one in-place field changed).
type KMSInPlaceUpdateScenario struct {
	BasicScenario
	Provider        EncryptionProvider
	UpdatedProvider EncryptionProvider
	// WaitForPropagation is called after the in-place update to verify the change
	// took effect. Receives the current encryption key so callers can match pod
	// container names to the active key. Same pattern as WaitForStuck in
	// KMSInvalidEncryptionRecoveryScenario.
	WaitForPropagation func(ctx context.Context, t testing.TB, keyMeta EncryptionKeyMeta)
}

// TestKMSInPlaceUpdate validates in-place KMS config field updates:
//  1. Apply valid provider and verify migration
//  2. Update in-place field and verify no new encryption key is created
//  3. WaitForPropagation — caller verifies the change took effect
func TestKMSInPlaceUpdate(ctx context.Context, t testing.TB, scenario KMSInPlaceUpdateScenario) {
	e := NewE(t, PrintEventsOnFailure(scenario.OperatorNamespace))
	clientSet := GetClients(e)

	require.NotNil(t, scenario.Provider.Setup, "Provider.Setup must not be nil")
	require.NotNil(t, scenario.UpdatedProvider.Setup, "UpdatedProvider.Setup must not be nil")
	require.NotNil(t, scenario.WaitForPropagation, "WaitForPropagation must not be nil")
	require.Equal(t, configv1.EncryptionTypeKMS, scenario.Provider.Type, "Provider must use KMS encryption type")
	require.Equal(t, configv1.EncryptionTypeKMS, scenario.UpdatedProvider.Type, "UpdatedProvider must use KMS encryption type")

	steps := []testStep{
		{name: "ApplyValidProviderAndVerifyMigration", testFunc: func(t testing.TB) {
			SetAndWaitForEncryptionType(ctx, t, scenario.Provider, scenario.TargetGRs,
				scenario.Namespace, scenario.LabelSelector)
			scenario.AssertFunc(t, clientSet, scenario.Provider.Type,
				scenario.Namespace, scenario.LabelSelector)
		}},
		{name: "UpdateInPlaceField", testFunc: func(t testing.TB) {
			keyMeta, err := GetLastKeyMeta(t, clientSet.Kube,
				scenario.Namespace, scenario.LabelSelector)
			require.NoError(t, err)
			scenario.UpdatedProvider.Setup(ctx, t)
			ApplyEncryption(ctx, t, scenario.UpdatedProvider.APIServerEncryption)
			WaitForNoNewEncryptionKey(t, clientSet.Kube, keyMeta,
				scenario.Namespace, scenario.LabelSelector)
		}},
		{name: "WaitForPropagation", testFunc: func(t testing.TB) {
			keyMeta, err := GetLastKeyMeta(t, clientSet.Kube,
				scenario.Namespace, scenario.LabelSelector)
			require.NoError(t, err)
			scenario.WaitForPropagation(ctx, t, keyMeta)
		}},
	}

	for _, step := range steps {
		t.Logf("=== STEP: %s ===", step.name)
		step.testFunc(e)
		if t.Failed() {
			t.Errorf("stopping the test as %q step failed", step.name)
			return
		}
	}
}
