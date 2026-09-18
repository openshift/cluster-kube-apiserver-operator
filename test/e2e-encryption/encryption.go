package e2e_encryption

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	g "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	library "github.com/openshift/library-go/test/library/encryption"
)

func init() {
	// Guard against "flag redefined" panics: multiple test packages (e.g. e2e-encryption-perf)
	// share this flag name.  When linked into the same OTE binary via dependencymagnet.go, every
	// package's init runs; the guard ensures the flag is registered exactly once.
	if flag.Lookup("provider") == nil {
		flag.String("provider", "", "encryption provider used by the tests (required when ENCRYPTION_PROVIDER env var is not set)")
	}
}

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("TestEncryptionTypeIdentity [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTypeIdentity(ctx, g.GinkgoTB())
	})
	g.It("TestEncryptionTypeUnset [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTypeUnset(ctx, g.GinkgoTB())
	})
	g.It("TestEncryptionTurnOnAndOff [Serial][Timeout:120m][Suite:encryption]", func(ctx context.Context) {
		testEncryptionTurnOnAndOff(ctx, g.GinkgoTB())
	})
})

// resolveEncryptionProvider returns the encryption type from the ENCRYPTION_PROVIDER
// env var (used in OTE/CI), falling back to the -provider flag (used in Makefile runs).
// It fails the test immediately when neither source is set, preventing a silent run
// against an unintended or zero-value provider.
func resolveEncryptionProvider(t testing.TB) configv1.EncryptionType {
	t.Helper()
	if env := os.Getenv("ENCRYPTION_PROVIDER"); env != "" {
		return configv1.EncryptionType(env)
	}
	if f := flag.Lookup("provider"); f != nil && f.Value.String() != "" {
		return configv1.EncryptionType(f.Value.String())
	}
	t.Fatal("encryption provider not set: supply the ENCRYPTION_PROVIDER env var (OTE/CI) or the -provider flag (Makefile)")
	return "" // unreachable; satisfies the compiler
}

func testEncryptionTypeIdentity(ctx context.Context, t testing.TB) {
	library.TestEncryptionTypeIdentity(ctx, t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func testEncryptionTypeUnset(ctx context.Context, t testing.TB) {
	library.TestEncryptionTypeUnset(ctx, t, library.BasicScenario{
		Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
		LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
		EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
		EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		OperatorNamespace:               operatorclient.OperatorNamespace,
		TargetGRs:                       library.WellKnownKASTargetGRs,
		AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
	})
}

func testEncryptionTurnOnAndOff(ctx context.Context, t testing.TB) {
	encType := resolveEncryptionProvider(t)
	t.Logf("encryption type: %s\n", encType)
	library.TestEncryptionTurnOnAndOff(ctx, t, library.OnOffScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       library.WellKnownKASTargetGRs,
			AssertFunc:                      library.AssertWellKnownSecretsAndConfigMaps,
		},
		CreateResourceFunc:             library.CreateAndStoreWellKnownSecretOfLife,
		AssertResourceEncryptedFunc:    library.AssertWellKnownSecretOfLifeEncrypted,
		AssertResourceNotEncryptedFunc: library.AssertWellKnownSecretOfLifeNotEncrypted,
		ResourceFunc:                   library.WellKnownSecretOfLife,
		ResourceName:                   "SecretOfLife",
		EncryptionProvider:             library.EncryptionProvider{APIServerEncryption: configv1.APIServerEncryption{Type: encType}},
	})
}
