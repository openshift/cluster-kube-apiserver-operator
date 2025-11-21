// Package adapter registers standard Go tests with OTE framework.
// This file uses embedded metadata to register tests without runtime file access.
package adapter

// Pre-discovered test metadata - embedded in binary at build time.
// To regenerate: developers run discovery locally and update this list.
var standardGoTestMetadata = []GoTestConfig{
	// test/e2e/operator_test.go
	{TestFile: "test/e2e/operator_test.go", TestPattern: "TestOperatorNamespace", Tags: []string{"Serial"}},
	{TestFile: "test/e2e/operator_test.go", TestPattern: "TestOperandImageVersion", Tags: []string{"Serial"}},
	{TestFile: "test/e2e/operator_test.go", TestPattern: "TestRevisionLimits", Tags: []string{"Serial"}},

	// test/e2e/bound_sa_token_test.go
	{TestFile: "test/e2e/bound_sa_token_test.go", TestPattern: "TestBoundTokenSignerController", Tags: []string{"Serial"}},
	{TestFile: "test/e2e/bound_sa_token_test.go", TestPattern: "TestTokenRequestAndReview", Tags: []string{"Serial"}},

	// test/e2e/certrotation_test.go
	{TestFile: "test/e2e/certrotation_test.go", TestPattern: "TestCertRotationTimeUpgradeable", Tags: []string{"Serial"}},
	{TestFile: "test/e2e/certrotation_test.go", TestPattern: "TestCertRotationStompOnBadType", Tags: []string{"Serial"}},

	// test/e2e/deprecated_api_test.go
	{TestFile: "test/e2e/deprecated_api_test.go", TestPattern: "TestAPIRemovedInNextReleaseInUse", Tags: []string{"Serial"}},
	{TestFile: "test/e2e/deprecated_api_test.go", TestPattern: "TestAPIRemovedInNextEUSReleaseInUse", Tags: []string{"Serial"}},

	// test/e2e/encryption_test.go
	{TestFile: "test/e2e/encryption_test.go", TestPattern: "TestEncryptionTypeAESCBC", Tags: []string{"Serial"}},

	// test/e2e/serviceaccountissuer_test.go
	{TestFile: "test/e2e/serviceaccountissuer_test.go", TestPattern: "TestServiceAccountIssuer", Tags: []string{"Serial"}},

	// test/e2e/user_certs_test.go
	{TestFile: "test/e2e/user_certs_test.go", TestPattern: "TestNamedCertificates", Tags: []string{"Serial"}},

	// test/e2e/user_client_ca_test.go
	{TestFile: "test/e2e/user_client_ca_test.go", TestPattern: "TestUserClientCABundle", Tags: []string{"Serial"}},

	// test/e2e/user_cors_test.go
	{TestFile: "test/e2e/user_cors_test.go", TestPattern: "TestAdditionalCORSAllowedOrigins", Tags: []string{"Serial"}},

	// test/e2e-encryption/encryption_test.go
	{TestFile: "test/e2e-encryption/encryption_test.go", TestPattern: "TestEncryptionTypeIdentity", Tags: []string{"Serial"}},
	{TestFile: "test/e2e-encryption/encryption_test.go", TestPattern: "TestEncryptionTypeUnset", Tags: []string{"Serial"}},
	{TestFile: "test/e2e-encryption/encryption_test.go", TestPattern: "TestEncryptionTurnOnAndOff", Tags: []string{"Serial"}},

	// test/e2e-encryption-perf/encryption_perf_test.go
	{TestFile: "test/e2e-encryption-perf/encryption_perf_test.go", TestPattern: "TestPerfEncryption", Tags: []string{"Serial"}},

	// test/e2e-encryption-rotation/encryption_rotation_test.go
	{TestFile: "test/e2e-encryption-rotation/encryption_rotation_test.go", TestPattern: "TestEncryptionRotation", Tags: []string{"Serial"}},

	// test/e2e-sno-disruptive/sno_disruptive_test.go
	{TestFile: "test/e2e-sno-disruptive/sno_disruptive_test.go", TestPattern: "TestFallback", Tags: []string{"Serial"}},
}

// Register standard Go tests with Ginkgo/OTE.
// This var _ declaration runs at package import time, registering tests before main() runs.
// Similar to how Ginkgo tests use var _ = g.Describe(...).
var _ = RunGoTestSuite(GoTestSuite{
	Description: "[sig-api-machinery] kube-apiserver operator Standard Go Tests",
	TestFiles:   standardGoTestMetadata,
})
