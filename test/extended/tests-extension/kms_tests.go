package extended

import (
	"context"
	"fmt"
	"os"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// findExistingKMSKey attempts to find an existing KMS key configured in the APIServer
func findExistingKMSKey(ctx context.Context, node ComputeNode) (string, error) {
	// This is a placeholder that would check if KMS is already configured
	// In a real implementation, this would check the apiserver CR for existing KMS config
	// For now, we'll return empty to indicate no existing key found
	return "", fmt.Errorf("no existing KMS key found")
}

var _ = g.Describe("[Jira:kube-apiserver][sig-api-machinery] API Server KMS", func() {
	var (
		kubeClient    *kubernetes.Clientset
		dynamicClient dynamic.Interface
		ctx           context.Context
		tmpdir        string

		// Suite-level shared resources
		kmsKeyArn             string
		kmsRegion             string
		masterNode            ComputeNode
		featureGateWasEnabled bool
	)

	// BeforeSuite runs once before all tests in this suite
	g.BeforeEach(func() {
		if kubeClient != nil {
			return // Already initialized
		}

		ctx = context.Background()
		Logf("\n╔════════════════════════════════════════════════════════════╗")
		Logf("║            KMS TEST SUITE INITIALIZATION                   ║")
		Logf("╚════════════════════════════════════════════════════════════╝\n")

		// Create temporary directory for test files
		var err error
		tmpdir, err = os.MkdirTemp("", "kms-test-*")
		o.Expect(err).NotTo(o.HaveOccurred())
		Logf("[Suite-Setup] Created temporary directory: %s", tmpdir)

		// Get kubeconfig and create clients
		kubeconfig := GetKubeConfig()
		kubeClient, dynamicClient = CreateKubernetesClients(kubeconfig)

		// Check cluster health
		g.By("Checking cluster health before KMS test suite")
		Logf("[Suite-Setup] Performing cluster health check...")
		err = waitForClusterStable(ctx, dynamicClient)
		if err != nil {
			Logf("[Suite-Setup] Cluster health check failed: %v", err)
			g.Skip(fmt.Sprintf("Cluster health check failed: %s", err))
		}
		Logf("[Suite-Setup] ✓ Cluster is healthy")

		// Step 1: Check and enable KMS feature gate
		Logf("\n--- Step 1: Feature Gate Configuration ---")
		g.By("Checking if KMSEncryptionProvider feature gate is enabled")

		isEnabled, err := isFeatureGateEnabled(ctx, dynamicClient, "KMSEncryptionProvider")
		o.Expect(err).NotTo(o.HaveOccurred())

		if isEnabled {
			Logf("[Suite-Setup] ✓ KMSEncryptionProvider is already enabled")
			featureGateWasEnabled = true
		} else {
			Logf("[Suite-Setup] KMSEncryptionProvider is not enabled, enabling now...")
			featureGateWasEnabled = false

			err = patchFeatureGate(ctx, dynamicClient, `{"spec":{"featureSet":"CustomNoUpgrade","customNoUpgrade":{"enabled":["KMSEncryptionProvider"],"disabled":[]}}}`)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Suite-Setup] ✓ Feature gate patch applied")

			// Wait for kube-apiserver to rollout
			g.By("Waiting for kube-apiserver operator to rollout after enabling KMS")
			expectedStatus := map[string]string{"Progressing": "True"}
			kubeApiserverCoStatus := map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}

			Logf("[Suite-Setup] Waiting for operator to start progressing (timeout: 300s)")
			err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 300, expectedStatus)
			if err != nil {
				Logf("[Suite-Setup] Warning: Operator did not start progressing: %v", err)
			}

			Logf("[Suite-Setup] Waiting for operator to become stable (timeout: 1800s)")
			err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 1800, kubeApiserverCoStatus)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Suite-Setup] ✓ kube-apiserver operator is stable after enabling feature gate")

			// Verify all cluster operators are still stable after feature gate change
			g.By("Verifying all cluster operators are stable after feature gate change")
			Logf("[Suite-Setup] Checking cluster stability after feature gate change...")
			err = waitForClusterStable(ctx, dynamicClient)
			if err != nil {
				Logf("[Suite-Setup] Cluster stability check failed: %v", err)
				o.Expect(err).NotTo(o.HaveOccurred())
			}
			Logf("[Suite-Setup] ✓ All cluster operators are stable after feature gate change")
		}

		// Step 2: Get master node
		Logf("\n--- Step 2: Master Node Discovery ---")
		g.By("Getting master nodes")
		nodes, cleanup := GetNodes(ctx, kubeClient, dynamicClient, "master")
		if cleanup != nil {
			g.DeferCleanup(cleanup)
		}
		o.Expect(len(nodes)).To(o.BeNumerically(">", 0), "No master nodes found")
		masterNode = nodes[0]
		Logf("[Suite-Setup] ✓ Using master node: %s", masterNode.GetName())

		// Step 3: Check and create KMS key
		Logf("\n--- Step 3: KMS Key Configuration ---")
		g.By("Checking if KMS key already exists")

		existingKeyArn, err := findExistingKMSKey(ctx, masterNode)
		if err == nil && existingKeyArn != "" {
			Logf("[Suite-Setup] ✓ Found existing KMS key: %s", existingKeyArn)
			kmsKeyArn = existingKeyArn
		} else {
			Logf("[Suite-Setup] No existing KMS key found, creating new one...")
			kmsKeyArn = masterNode.CreateKMSKey()
			o.Expect(kmsKeyArn).NotTo(o.BeEmpty())
			Logf("[Suite-Setup] ✓ Created KMS key: %s", kmsKeyArn)

			Logf("[Suite-Setup] Updating KMS key policy...")
			masterNode.UpdateKmsPolicy(kmsKeyArn)
			Logf("[Suite-Setup] ✓ KMS key policy updated")
		}

		// Extract region from ARN
		kmsRegion = masterNode.GetRegionFromARN(kmsKeyArn)
		Logf("[Suite-Setup] ✓ KMS region: %s", kmsRegion)

		Logf("\n╔════════════════════════════════════════════════════════════╗")
		Logf("║       KMS TEST SUITE INITIALIZATION COMPLETE               ║")
		Logf("║  Feature Gate: %s                                         ║", getStatusString(isEnabled))
		Logf("║  KMS Key ARN: %s", truncateString(kmsKeyArn, 45))
		Logf("║  KMS Region: %-47s║", kmsRegion)
		Logf("╚════════════════════════════════════════════════════════════╝\n")

		// Register cleanup to run after all tests complete
		g.DeferCleanup(func() {
			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║              KMS TEST SUITE CLEANUP                        ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")

			if tmpdir != "" {
				os.RemoveAll(tmpdir)
				Logf("[Suite-Cleanup] Cleaned up temporary directory: %s", tmpdir)
			}

			// Delete KMS key if it was created
			if kmsKeyArn != "" {
				Logf("[Suite-Cleanup] Deleting KMS key: %s", kmsKeyArn)
				masterNode.DeleteKMSKey(kmsKeyArn)
			}

			// Only disable feature gate if we enabled it
			if !featureGateWasEnabled && dynamicClient != nil {
				Logf("[Suite-Cleanup] Disabling KMSEncryptionProvider feature gate...")
				err := patchFeatureGate(ctx, dynamicClient, `{"spec":{"featureSet":"CustomNoUpgrade","customNoUpgrade":{"enabled":[],"disabled":["KMSEncryptionProvider"]}}}`)
				if err != nil {
					Logf("[Suite-Cleanup] Warning: Failed to disable feature gate: %v", err)
				} else {
					Logf("[Suite-Cleanup] ✓ Feature gate disabled")

					// Wait for rollout
					expectedStatus := map[string]string{"Progressing": "True"}
					kubeApiserverCoStatus := map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}

					Logf("[Suite-Cleanup] Waiting for operator to stabilize (timeout: 1800s)")
					err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 300, expectedStatus)
					if err != nil {
						Logf("[Suite-Cleanup] Warning: Operator did not start progressing: %v", err)
					}
					err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 1800, kubeApiserverCoStatus)
					if err != nil {
						Logf("[Suite-Cleanup] Warning: Operator did not stabilize: %v", err)
					} else {
						Logf("[Suite-Cleanup] ✓ Operator is stable")

						// Verify all cluster operators are stable after disabling feature gate
						Logf("[Suite-Cleanup] Checking cluster stability after disabling feature gate...")
						err = waitForClusterStable(ctx, dynamicClient)
						if err != nil {
							Logf("[Suite-Cleanup] Warning: Cluster stability check failed: %v", err)
						} else {
							Logf("[Suite-Cleanup] ✓ All cluster operators are stable after cleanup")
						}
					}
				}
			}

			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║           KMS TEST SUITE CLEANUP COMPLETE                  ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")
		})
	})

	g.It("should validate KMS encryption configuration [Suite:openshift/cluster-kube-apiserver-operator/kms] [Timeout:30m]",
		func() {
			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║     KMS ENCRYPTION CONFIGURATION VALIDATION TEST           ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")

			Logf("\n--- Phase 1: Validate KMS Configuration Errors ---")
			g.By("Loading KMS test cases from YAML")
			Logf("[Phase 1] Loading test cases from YAML file")

			testCases, err := masterNode.LoadKMSTestCasesFromYAML()
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Phase 1] Loaded %d test case(s)", len(testCases))

			g.By("Running KMS validation test cases")
			for i, tc := range testCases {
				Logf("\n[Phase 1] Test Case %d/%d: %s", i+1, len(testCases), tc.Name)
				g.By(fmt.Sprintf("Testing: %s", tc.Name))
				Logf("[Phase 1]   Expected error: %s", tc.ExpectedError)

				// Try to apply the config - should fail with expected error
				err = applyAPIServerConfig(ctx, dynamicClient, []byte(tc.Initial))
				if err == nil {
					Logf("[Phase 1]   ✗ FAILED: Expected validation error but got success")
				} else {
					Logf("[Phase 1]   Actual error: %s", err.Error())
				}

				o.Expect(err).To(o.HaveOccurred(), "Expected validation error for test case: %s", tc.Name)
				o.Expect(err.Error()).To(o.ContainSubstring(tc.ExpectedError),
					"Error message should contain expected validation error")

				Logf("[Phase 1]   ✓ Validation passed")
				g.By(fmt.Sprintf("✓ Validation passed for: %s", tc.Name))
			}
			Logf("[Phase 1] ✓ All %d validation test cases passed", len(testCases))

			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║   ✓ KMS ENCRYPTION VALIDATION COMPLETED SUCCESSFULLY       ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")
			g.By("KMS encryption configuration validation completed successfully")
		})

	g.It("should encrypt secrets using KMS [Suite:openshift/cluster-kube-apiserver-operator/kms] [Timeout:60m][Serial][Disruptive]",
		func() {
			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║          KMS SECRET ENCRYPTION VERIFICATION TEST           ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")

			var (
				expectedStatus        = map[string]string{"Progressing": "True"}
				kubeApiserverCoStatus = map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}
			)

			Logf("\n--- Phase 1: Apply KMS Encryption Configuration ---")
			g.By("Configuring KMS encryption for API server")
			Logf("[Phase 1] Using shared KMS key: %s", kmsKeyArn)
			Logf("[Phase 1] Using region: %s", kmsRegion)

			// Check and apply KMS config if needed
			needsRollout, err := checkAndApplyKMSConfig(ctx, dynamicClient, kmsKeyArn, kmsRegion)
			o.Expect(err).NotTo(o.HaveOccurred())

			if needsRollout {
				g.By("Waiting for kube-apiserver operator to rollout")
				Logf("[Phase 1] Waiting for kube-apiserver to start progressing (timeout: 300s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 300, expectedStatus)
				if err != nil {
					Logf("[Phase 1] Warning: Operator did not start progressing: %v", err)
				}

				Logf("[Phase 1] Waiting for kube-apiserver to become stable (timeout: 1800s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 1800, kubeApiserverCoStatus)
				o.Expect(err).NotTo(o.HaveOccurred(), "kube-apiserver operator did not become stable after KMS configuration")
				Logf("[Phase 1] ✓ kube-apiserver operator is stable")

				// Check KMS plugin health
				Logf("[Phase 1] Checking KMS plugin health...")
				isHealthy, healthMsg := checkKMSPluginHealth(ctx, dynamicClient)
				if !isHealthy {
					Logf("[Phase 1] Warning: KMS plugin health check: %s", healthMsg)
					Logf("[Phase 1] Note: KMS socket errors during initial rollout are expected and should resolve")
				} else {
					Logf("[Phase 1] ✓ KMS plugin health check: %s", healthMsg)
				}
			} else {
				Logf("[Phase 1] ✓ KMS already configured, no rollout needed")
			}

			// Verify all cluster operators are still stable after KMS configuration
			if needsRollout {
				g.By("Verifying all cluster operators are stable after KMS configuration")
				Logf("[Phase 1] Checking cluster stability after KMS configuration...")
				err = waitForClusterStable(ctx, dynamicClient)
				if err != nil {
					Logf("[Phase 1] Cluster stability check failed: %v", err)
					o.Expect(err).NotTo(o.HaveOccurred())
				}
				Logf("[Phase 1] ✓ All cluster operators are stable after KMS configuration")
			}

			Logf("\n--- Phase 2: Verify Encryption Type ---")
			g.By("Verifying encryption type is KMS")
			encType, encCompleted := masterNode.VerifyEncryptionType(ctx, dynamicClient)
			o.Expect(encType).To(o.Equal("KMS"), "Encryption type should be KMS")
			Logf("[Phase 2] ✓ Encryption type: %s", encType)
			Logf("[Phase 2] ✓ Encryption completed: %v", encCompleted)

			Logf("\n--- Phase 3: Create Test Secret ---")
			g.By("Creating test namespace and secret")
			testNamespace := "kms-secret-test"
			Logf("[Phase 3] Creating namespace: %s", testNamespace)

			err = createNamespace(ctx, kubeClient, testNamespace)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Phase 3] ✓ Namespace created")

			defer func() {
				Logf("[Cleanup] Deleting test namespace: %s", testNamespace)
				deleteNamespace(ctx, kubeClient, testNamespace)
			}()

			secretName := "mysecret1"
			secretData := map[string]string{"password": "SuperSecure123"}
			Logf("[Phase 3] Creating secret: %s", secretName)
			err = createSecret(ctx, kubeClient, testNamespace, secretName, secretData)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Phase 3] ✓ Secret created successfully")

			Logf("\n--- Phase 4: Verify Secret Encryption in etcd ---")
			g.By("Verifying secret is encrypted with KMSv2 in etcd")
			Logf("[Phase 4] Checking etcd encryption format for secret")

			isEncrypted, encryptionFormat := masterNode.VerifySecretEncryption(ctx, testNamespace, secretName)
			o.Expect(isEncrypted).To(o.BeTrue(), "Secret should be encrypted in etcd")
			o.Expect(encryptionFormat).To(o.Equal("k8s:enc:kms:v2:"), "Secret should use KMSv2 encryption format")
			Logf("[Phase 4] ✓ Secret is encrypted with format: %s", encryptionFormat)

			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║      ✓ KMS SECRET ENCRYPTION VERIFIED SUCCESSFULLY        ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")
		})

	g.It("should encrypt OAuthAccessTokens using KMS [Suite:openshift/cluster-kube-apiserver-operator/kms] [Timeout:120m][Serial][Disruptive]",
		func() {
			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║     KMS OAUTH ACCESS TOKEN ENCRYPTION VERIFICATION        ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")

			var (
				expectedStatus        = map[string]string{"Progressing": "True"}
				kubeApiserverCoStatus = map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}
				userUID               string
			)

			Logf("\n--- Phase 1: Ensure KMS Encryption Configuration ---")
			g.By("Configuring KMS encryption for API server")
			Logf("[Phase 1] Using shared KMS key: %s", kmsKeyArn)
			Logf("[Phase 1] Using region: %s", kmsRegion)

			// Check and apply KMS config if needed
			needsRollout, err := checkAndApplyKMSConfig(ctx, dynamicClient, kmsKeyArn, kmsRegion)
			o.Expect(err).NotTo(o.HaveOccurred())

			if needsRollout {
				g.By("Waiting for kube-apiserver operator to rollout")
				Logf("[Phase 1] Waiting for kube-apiserver to start progressing (timeout: 300s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 300, expectedStatus)
				if err != nil {
					Logf("[Phase 1] Warning: Operator did not start progressing: %v", err)
				}

				Logf("[Phase 1] Waiting for kube-apiserver to become stable (timeout: 1800s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 1800, kubeApiserverCoStatus)
				o.Expect(err).NotTo(o.HaveOccurred(), "kube-apiserver operator did not become stable after KMS configuration")
				Logf("[Phase 1] ✓ kube-apiserver operator is stable")

				// Check KMS plugin health
				Logf("[Phase 1] Checking KMS plugin health...")
				isHealthy, healthMsg := checkKMSPluginHealth(ctx, dynamicClient)
				if !isHealthy {
					Logf("[Phase 1] Warning: KMS plugin health check: %s", healthMsg)
					Logf("[Phase 1] Note: KMS socket errors during initial rollout are expected and should resolve")
				} else {
					Logf("[Phase 1] ✓ KMS plugin health check: %s", healthMsg)
				}

				// Verify all cluster operators are still stable after KMS configuration
				g.By("Verifying all cluster operators are stable after KMS configuration")
				Logf("[Phase 1] Checking cluster stability after KMS configuration...")
				err = waitForClusterStable(ctx, dynamicClient)
				if err != nil {
					Logf("[Phase 1] Cluster stability check failed: %v", err)
					o.Expect(err).NotTo(o.HaveOccurred())
				}
				Logf("[Phase 1] ✓ All cluster operators are stable after KMS configuration")
			} else {
				Logf("[Phase 1] ✓ KMS already configured, no rollout needed")
			}

			Logf("\n--- Phase 2: Generate Token and Resource Name ---")
			g.By("Generating secure token and resource name")

			tokenValue, tokenResourceName := generateSecureToken()
			Logf("[Phase 2] ✓ Generated token value: %s", maskToken(tokenValue))
			Logf("[Phase 2] ✓ Token resource name: %s", tokenResourceName)

			Logf("\n--- Phase 3: Get Test User Information ---")
			g.By("Getting test user UID")
			testUser := "test-user-01"
			userUID, err = getUserUID(ctx, dynamicClient, testUser)
			if err != nil {
				Logf("[Phase 3] Test user not found, creating user")
				userUID = createTestUser(ctx, dynamicClient, testUser)
			}
			o.Expect(userUID).NotTo(o.BeEmpty())
			Logf("[Phase 3] ✓ Test user UID: %s", userUID)

			Logf("\n--- Phase 4: Create OAuthAccessToken ---")
			g.By("Creating OAuthAccessToken")

			accessToken := createOAuthAccessToken(tokenResourceName, tokenValue, testUser, userUID)
			Logf("[Phase 4] Creating access token: %s", tokenResourceName)

			err = applyOAuthToken(ctx, dynamicClient, "oauthaccesstokens", accessToken)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Phase 4] ✓ OAuthAccessToken created successfully")

			defer func() {
				Logf("[Cleanup] Deleting OAuthAccessToken: %s", tokenResourceName)
				deleteOAuthToken(ctx, dynamicClient, "oauthaccesstokens", tokenResourceName)
			}()

			Logf("\n--- Phase 5: Verify Token Encryption in etcd ---")
			g.By("Verifying OAuthAccessToken is encrypted with KMSv2 in etcd")

			isEncrypted, encryptionFormat := masterNode.VerifyOAuthTokenEncryption(ctx, "oauthaccesstokens", tokenResourceName)
			o.Expect(isEncrypted).To(o.BeTrue(), "OAuthAccessToken should be encrypted in etcd")
			o.Expect(encryptionFormat).To(o.Equal("k8s:enc:kms:v2:"), "OAuthAccessToken should use KMSv2 encryption format")
			Logf("[Phase 5] ✓ OAuthAccessToken is encrypted with format: %s", encryptionFormat)

			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║   ✓ OAUTH ACCESS TOKEN ENCRYPTION VERIFIED SUCCESSFULLY   ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")
		})

	g.It("should encrypt OAuthAuthorizeTokens using KMS [Suite:openshift/cluster-kube-apiserver-operator/kms] [Timeout:120m][Serial][Disruptive]",
		func() {
			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║    KMS OAUTH AUTHORIZE TOKEN ENCRYPTION VERIFICATION       ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")

			var (
				expectedStatus        = map[string]string{"Progressing": "True"}
				kubeApiserverCoStatus = map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}
				userUID               string
			)

			Logf("\n--- Phase 1: Ensure KMS Encryption Configuration ---")
			g.By("Configuring KMS encryption for API server")
			Logf("[Phase 1] Using shared KMS key: %s", kmsKeyArn)
			Logf("[Phase 1] Using region: %s", kmsRegion)

			// Check and apply KMS config if needed
			needsRollout, err := checkAndApplyKMSConfig(ctx, dynamicClient, kmsKeyArn, kmsRegion)
			o.Expect(err).NotTo(o.HaveOccurred())

			if needsRollout {
				g.By("Waiting for kube-apiserver operator to rollout")
				Logf("[Phase 1] Waiting for kube-apiserver to start progressing (timeout: 300s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 300, expectedStatus)
				if err != nil {
					Logf("[Phase 1] Warning: Operator did not start progressing: %v", err)
				}

				Logf("[Phase 1] Waiting for kube-apiserver to become stable (timeout: 1800s)")
				err = waitForOperatorStatus(ctx, dynamicClient, "kube-apiserver", 1800, kubeApiserverCoStatus)
				o.Expect(err).NotTo(o.HaveOccurred(), "kube-apiserver operator did not become stable after KMS configuration")
				Logf("[Phase 1] ✓ kube-apiserver operator is stable")

				// Check KMS plugin health
				Logf("[Phase 1] Checking KMS plugin health...")
				isHealthy, healthMsg := checkKMSPluginHealth(ctx, dynamicClient)
				if !isHealthy {
					Logf("[Phase 1] Warning: KMS plugin health check: %s", healthMsg)
					Logf("[Phase 1] Note: KMS socket errors during initial rollout are expected and should resolve")
				} else {
					Logf("[Phase 1] ✓ KMS plugin health check: %s", healthMsg)
				}

				// Verify all cluster operators are still stable after KMS configuration
				g.By("Verifying all cluster operators are stable after KMS configuration")
				Logf("[Phase 1] Checking cluster stability after KMS configuration...")
				err = waitForClusterStable(ctx, dynamicClient)
				if err != nil {
					Logf("[Phase 1] Cluster stability check failed: %v", err)
					o.Expect(err).NotTo(o.HaveOccurred())
				}
				Logf("[Phase 1] ✓ All cluster operators are stable after KMS configuration")
			} else {
				Logf("[Phase 1] ✓ KMS already configured, no rollout needed")
			}

			Logf("\n--- Phase 2: Generate Auth Code and Resource Name ---")
			g.By("Generating secure authorization code and resource name")

			authCode, authResourceName := generateSecureToken()
			Logf("[Phase 2] ✓ Generated auth code: %s", maskToken(authCode))
			Logf("[Phase 2] ✓ Auth token resource name: %s", authResourceName)

			Logf("\n--- Phase 3: Get Test User Information ---")
			g.By("Getting test user UID")
			testUser := "test-user-01"
			userUID, err = getUserUID(ctx, dynamicClient, testUser)
			if err != nil {
				Logf("[Phase 3] Test user not found, creating user")
				userUID = createTestUser(ctx, dynamicClient, testUser)
			}
			o.Expect(userUID).NotTo(o.BeEmpty())
			Logf("[Phase 3] ✓ Test user UID: %s", userUID)

			Logf("\n--- Phase 4: Create OAuthAuthorizeToken ---")
			g.By("Creating OAuthAuthorizeToken")

			authorizeToken := createOAuthAuthorizeToken(authResourceName, authCode, testUser, userUID)
			Logf("[Phase 4] Creating authorize token: %s", authResourceName)

			err = applyOAuthToken(ctx, dynamicClient, "oauthauthorizetokens", authorizeToken)
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[Phase 4] ✓ OAuthAuthorizeToken created successfully")

			defer func() {
				Logf("[Cleanup] Deleting OAuthAuthorizeToken: %s", authResourceName)
				deleteOAuthToken(ctx, dynamicClient, "oauthauthorizetokens", authResourceName)
			}()

			Logf("\n--- Phase 5: Verify Token Encryption in etcd ---")
			g.By("Verifying OAuthAuthorizeToken is encrypted with KMSv2 in etcd")

			isEncrypted, encryptionFormat := masterNode.VerifyOAuthTokenEncryption(ctx, "oauthauthorizetokens", authResourceName)
			o.Expect(isEncrypted).To(o.BeTrue(), "OAuthAuthorizeToken should be encrypted in etcd")
			o.Expect(encryptionFormat).To(o.Equal("k8s:enc:kms:v2:"), "OAuthAuthorizeToken should use KMSv2 encryption format")
			Logf("[Phase 5] ✓ OAuthAuthorizeToken is encrypted with format: %s", encryptionFormat)

			Logf("\n╔════════════════════════════════════════════════════════════╗")
			Logf("║  ✓ OAUTH AUTHORIZE TOKEN ENCRYPTION VERIFIED SUCCESSFULLY ║")
			Logf("╚════════════════════════════════════════════════════════════╝\n")
		})
})
