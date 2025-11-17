package extended

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Logf logs formatted output to Ginkgo writer
func Logf(format string, args ...interface{}) {
	fmt.Fprintf(g.GinkgoWriter, format+"\n", args...)
}

// Failf fails the test with formatted message
func Failf(format string, args ...interface{}) {
	g.Fail(fmt.Sprintf(format, args...))
}

// GetKubeConfig gets KUBECONFIG from environment and validates it exists
func GetKubeConfig() string {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		Logf("[Setup] KUBECONFIG not set, skipping test")
		g.Skip("KUBECONFIG environment variable not set")
	}
	Logf("[Setup] Using KUBECONFIG: %s", kubeconfig)
	return kubeconfig
}

// CreateKubernetesClients creates Kubernetes and dynamic clients from kubeconfig
func CreateKubernetesClients(kubeconfig string) (*kubernetes.Clientset, dynamic.Interface) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		Failf("Failed to load Kubernetes config: %v", err)
	}
	Logf("[Setup] Kubernetes config loaded successfully")

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		Failf("Failed to create Kubernetes client: %v", err)
	}
	Logf("[Setup] Kubernetes client created")

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		Failf("Failed to create dynamic client: %v", err)
	}
	Logf("[Setup] Dynamic client created")

	return kubeClient, dynamicClient
}

// patchFeatureGate patches the cluster featuregate
func patchFeatureGate(ctx context.Context, client dynamic.Interface, patchData string) error {
	featureGateGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "featuregates",
	}

	_, err := client.Resource(featureGateGVR).Patch(ctx, "cluster", "application/merge-patch+json",
		[]byte(patchData), v1.PatchOptions{})

	return err
}

// applyAPIServerConfig attempts to apply an APIServer configuration
func applyAPIServerConfig(ctx context.Context, client dynamic.Interface, yamlData []byte) error {
	apiServerGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "apiservers",
	}

	// Parse YAML to get the desired spec
	var yamlObj interface{}
	err := yaml.Unmarshal(yamlData, &yamlObj)
	if err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	// Convert to unstructured
	yamlMap, err := convertToStringMap(yamlObj)
	if err != nil {
		return fmt.Errorf("failed to convert YAML to unstructured: %w", err)
	}

	// Get the existing APIServer resource to preserve metadata including resourceVersion
	existing, err := client.Resource(apiServerGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing apiserver: %w", err)
	}

	// Extract spec from YAML and set it on the existing resource
	if spec, found := yamlMap["spec"]; found {
		existing.Object["spec"] = spec
	}

	// Try to update the APIServer resource - this will trigger server-side validation
	_, err = client.Resource(apiServerGVR).Update(ctx, existing, v1.UpdateOptions{})
	return err
}

// convertToStringMap converts interface{} to map[string]interface{} recursively
func convertToStringMap(i interface{}) (map[string]interface{}, error) {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m := map[string]interface{}{}
		for k, v := range x {
			strKey, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("non-string key found: %v", k)
			}
			switch val := v.(type) {
			case map[interface{}]interface{}:
				converted, err := convertToStringMap(val)
				if err != nil {
					return nil, err
				}
				m[strKey] = converted
			case []interface{}:
				m[strKey] = convertSlice(val)
			default:
				m[strKey] = val
			}
		}
		return m, nil
	case map[string]interface{}:
		return x, nil
	default:
		return nil, fmt.Errorf("expected map, got %T", i)
	}
}

// convertSlice converts []interface{} recursively
func convertSlice(s []interface{}) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		switch val := v.(type) {
		case map[interface{}]interface{}:
			converted, _ := convertToStringMap(val)
			result[i] = converted
		case []interface{}:
			result[i] = convertSlice(val)
		default:
			result[i] = val
		}
	}
	return result
}

// waitForClusterStable waits for the cluster to be stable
// Checks that all cluster operators are Available=True, Progressing=False, Degraded=False
func waitForClusterStable(ctx context.Context, client dynamic.Interface) error {
	coGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusteroperators",
	}

	// Wait for all COs to be stable
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 18*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			coList, err := client.Resource(coGVR).List(ctx, v1.ListOptions{})
			if err != nil {
				return false, nil
			}

			unstableCOs := []string{}
			for _, item := range coList.Items {
				coName := item.GetName()
				conditions, found, err := unstructured.NestedSlice(item.Object, "status", "conditions")
				if !found || err != nil {
					unstableCOs = append(unstableCOs, coName)
					continue
				}

				currentStatus := make(map[string]string)
				for _, cond := range conditions {
					condition := cond.(map[string]interface{})
					condType := condition["type"].(string)
					status := condition["status"].(string)
					currentStatus[condType] = status
				}

				// Check if CO is stable (Available=True, Progressing=False, Degraded=False)
				if currentStatus["Available"] != "True" ||
					currentStatus["Progressing"] != "False" ||
					currentStatus["Degraded"] != "False" {
					unstableCOs = append(unstableCOs, coName)
				}
			}

			if len(unstableCOs) > 0 {
				return false, nil
			}

			return true, nil
		})
}

// waitForOperatorStatus waits for an operator to reach the expected status
func waitForOperatorStatus(ctx context.Context, client dynamic.Interface, operatorName string, timeoutSeconds int, expectedStatus map[string]string) error {
	coGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusteroperators",
	}

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, time.Duration(timeoutSeconds)*time.Second, true,
		func(ctx context.Context) (bool, error) {
			co, err := client.Resource(coGVR).Get(ctx, operatorName, v1.GetOptions{})
			if err != nil {
				return false, nil
			}

			conditions, found, err := unstructured.NestedSlice(co.Object, "status", "conditions")
			if !found || err != nil {
				return false, nil
			}

			currentStatus := make(map[string]string)
			for _, cond := range conditions {
				condition := cond.(map[string]interface{})
				condType := condition["type"].(string)
				status := condition["status"].(string)
				currentStatus[condType] = status
			}

			// Check if current status matches expected status
			for expectedType, expectedValue := range expectedStatus {
				if currentStatus[expectedType] != expectedValue {
					return false, nil
				}
			}

			return true, nil
		})
}

// checkAndApplyKMSConfig checks if KMS config is already applied and correct, applies if needed
// Returns: (needsRollout bool, error)
func checkAndApplyKMSConfig(ctx context.Context, client dynamic.Interface, expectedKeyARN, expectedRegion string) (bool, error) {
	apiServerGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "apiservers",
	}

	// Get current apiserver config
	apiServer, err := client.Resource(apiServerGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get apiserver config: %w", err)
	}

	// Check current encryption config
	encType, _, _ := unstructured.NestedString(apiServer.Object, "spec", "encryption", "type")
	if encType == "KMS" {
		// KMS is already configured, verify it's correct
		kmsType, _, _ := unstructured.NestedString(apiServer.Object, "spec", "encryption", "kms", "type")
		if kmsType == "AWS" {
			currentKeyARN, _, _ := unstructured.NestedString(apiServer.Object, "spec", "encryption", "kms", "aws", "keyARN")
			currentRegion, _, _ := unstructured.NestedString(apiServer.Object, "spec", "encryption", "kms", "aws", "region")

			if currentKeyARN == expectedKeyARN && currentRegion == expectedRegion {
				Logf("[KMS-Config] ✓ KMS is already configured correctly")
				Logf("[KMS-Config]   Key ARN: %s", currentKeyARN)
				Logf("[KMS-Config]   Region: %s", currentRegion)
				return false, nil // No need to apply, already correct
			}

			Logf("[KMS-Config] KMS is configured but with different values")
			Logf("[KMS-Config]   Current Key ARN: %s", currentKeyARN)
			Logf("[KMS-Config]   Expected Key ARN: %s", expectedKeyARN)
			Logf("[KMS-Config]   Current Region: %s", currentRegion)
			Logf("[KMS-Config]   Expected Region: %s", expectedRegion)
		}
	} else if encType != "" {
		Logf("[KMS-Config] Current encryption type: %s (not KMS)", encType)
	} else {
		Logf("[KMS-Config] No encryption currently configured")
	}

	// Apply KMS configuration
	Logf("[KMS-Config] Applying KMS encryption configuration...")
	kmsConfig := fmt.Sprintf(`{
		"spec": {
			"encryption": {
				"type": "KMS",
				"kms": {
					"type": "AWS",
					"aws": {
						"keyARN": "%s",
						"region": "%s"
					}
				}
			}
		}
	}`, expectedKeyARN, expectedRegion)

	err = patchAPIServerConfig(ctx, client, kmsConfig)
	if err != nil {
		return false, fmt.Errorf("failed to apply KMS config: %w", err)
	}

	Logf("[KMS-Config] ✓ KMS configuration applied")
	return true, nil // Config was applied, rollout needed
}

// patchAPIServerConfig patches the API server configuration
func patchAPIServerConfig(ctx context.Context, client dynamic.Interface, patchData string) error {
	apiServerGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "apiservers",
	}

	_, err := client.Resource(apiServerGVR).Patch(ctx, "cluster", "application/merge-patch+json",
		[]byte(patchData), v1.PatchOptions{})

	return err
}

// createNamespace creates a namespace
func createNamespace(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: namespace,
		},
	}
	_, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, v1.CreateOptions{})
	return err
}

// deleteNamespace deletes a namespace
func deleteNamespace(ctx context.Context, kubeClient *kubernetes.Clientset, namespace string) error {
	return kubeClient.CoreV1().Namespaces().Delete(ctx, namespace, v1.DeleteOptions{})
}

// createSecret creates a secret in the specified namespace
func createSecret(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, name string, data map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: data,
		Type:       corev1.SecretTypeOpaque,
	}
	_, err := kubeClient.CoreV1().Secrets(namespace).Create(ctx, secret, v1.CreateOptions{})
	return err
}

// generateSecureToken generates a secure token and its resource name
// Returns: (tokenValue, tokenResourceName)
func generateSecureToken() (string, string) {
	// Generate 32-byte random token
	rawToken := make([]byte, 32)
	_, err := rand.Read(rawToken)
	if err != nil {
		Failf("Failed to generate random token: %v", err)
	}

	// Base64-URL encode the token
	tokenValue := base64.URLEncoding.EncodeToString(rawToken)
	tokenValue = strings.TrimRight(tokenValue, "=")

	// Calculate SHA256 hash of token
	hash := sha256.Sum256([]byte(tokenValue))

	// Base64-URL encode the hash
	tokenHash := base64.URLEncoding.EncodeToString(hash[:])
	tokenHash = strings.TrimRight(tokenHash, "=")

	// Create resource name with sha256~ prefix
	resourceName := "sha256~" + tokenHash

	return tokenValue, resourceName
}

// maskToken masks a token for logging (shows first and last 4 characters)
func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// getUserUID gets the UID of a user
func getUserUID(ctx context.Context, client dynamic.Interface, userName string) (string, error) {
	userGVR := schema.GroupVersionResource{
		Group:    "user.openshift.io",
		Version:  "v1",
		Resource: "users",
	}

	user, err := client.Resource(userGVR).Get(ctx, userName, v1.GetOptions{})
	if err != nil {
		return "", err
	}

	uid, found, err := unstructured.NestedString(user.Object, "metadata", "uid")
	if !found || err != nil {
		return "", fmt.Errorf("uid not found in user object")
	}

	return uid, nil
}

// createTestUser creates a test user (simplified - in real cluster this would be done via IDP)
func createTestUser(ctx context.Context, client dynamic.Interface, userName string) string {
	// In a real cluster, users come from IDP
	// For testing, we'll use a well-known test user UID
	// This is a placeholder - actual implementation depends on cluster setup
	Logf("Using placeholder UID for test user: %s", userName)
	return "00000000-0000-0000-0000-000000000001"
}

// createOAuthAccessToken creates an OAuthAccessToken object
func createOAuthAccessToken(resourceName, tokenValue, userName, userUID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "oauth.openshift.io/v1",
			"kind":       "OAuthAccessToken",
			"metadata": map[string]interface{}{
				"name": resourceName,
			},
			"clientName": "openshift-challenging-client",
			"userName":   userName,
			"userUID":    userUID,
			"scopes": []interface{}{
				"user:full",
			},
			"expiresIn":   86400, // 24 hours
			"redirectURI": "https://oauth-openshift.apps.example.com/oauth/token/implicit",
			"token":       tokenValue,
		},
	}
}

// createOAuthAuthorizeToken creates an OAuthAuthorizeToken object
func createOAuthAuthorizeToken(resourceName, authCode, userName, userUID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "oauth.openshift.io/v1",
			"kind":       "OAuthAuthorizeToken",
			"metadata": map[string]interface{}{
				"name": resourceName,
			},
			"clientName": "openshift-challenging-client",
			"userName":   userName,
			"userUID":    userUID,
			"scopes": []interface{}{
				"user:full",
			},
			"expiresIn":   300, // 5 minutes
			"redirectURI": "https://oauth-openshift.apps.example.com/oauth/token/implicit",
			"code":        authCode,
		},
	}
}

// applyOAuthToken applies an OAuth token (access or authorize)
func applyOAuthToken(ctx context.Context, client dynamic.Interface, resource string, token *unstructured.Unstructured) error {
	oauthGVR := schema.GroupVersionResource{
		Group:    "oauth.openshift.io",
		Version:  "v1",
		Resource: resource,
	}

	_, err := client.Resource(oauthGVR).Create(ctx, token, v1.CreateOptions{})
	return err
}

// deleteOAuthToken deletes an OAuth token
func deleteOAuthToken(ctx context.Context, client dynamic.Interface, resource, name string) error {
	oauthGVR := schema.GroupVersionResource{
		Group:    "oauth.openshift.io",
		Version:  "v1",
		Resource: resource,
	}

	return client.Resource(oauthGVR).Delete(ctx, name, v1.DeleteOptions{})
}

// checkKMSPluginHealth checks if KMS plugin pods are healthy in kube-apiserver
// Returns: (isHealthy, message)
func checkKMSPluginHealth(ctx context.Context, client dynamic.Interface) (bool, string) {
	kubeAPIServerGVR := schema.GroupVersionResource{
		Group:    "operator.openshift.io",
		Version:  "v1",
		Resource: "kubeapiservers",
	}

	kubeAPIServer, err := client.Resource(kubeAPIServerGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		return false, fmt.Sprintf("Failed to get kubeapiserver: %v", err)
	}

	// Check conditions for KMS health
	conditions, found, err := unstructured.NestedSlice(kubeAPIServer.Object, "status", "conditions")
	if !found || err != nil {
		return false, "KMS conditions not found in kubeapiserver status"
	}

	// Look for KMS-related error conditions
	for _, cond := range conditions {
		condition := cond.(map[string]interface{})
		condType, _ := condition["type"].(string)
		status, _ := condition["status"].(string)
		message, _ := condition["message"].(string)
		reason, _ := condition["reason"].(string)

		// Check for Degraded condition related to KMS
		if condType == "Degraded" && status == "True" {
			if strings.Contains(message, "kms-provider") || strings.Contains(message, "kms") {
				return false, fmt.Sprintf("KMS degraded: %s - %s", reason, message)
			}
		}

		// Check for KMSConnectionDegraded or similar conditions
		if strings.Contains(condType, "KMS") && status == "True" {
			return false, fmt.Sprintf("KMS condition %s: %s - %s", condType, reason, message)
		}
	}

	return true, "KMS plugin appears healthy"
}

// verifyEncryptionType verifies the encryption type configured in the APIServer
// This is a generic function that works across all cloud platforms
// Returns: (encryptionType, encryptionCompleted)
func verifyEncryptionType(ctx context.Context, client dynamic.Interface) (string, bool) {
	Logf("[Verify] Checking encryption configuration")

	apiServerGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "apiservers",
	}

	apiServer, err := client.Resource(apiServerGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		Logf("[Verify] Failed to get apiserver: %v", err)
		return "", false
	}

	// Get encryption type
	encType, found, err := unstructured.NestedString(apiServer.Object, "spec", "encryption", "type")
	if !found || err != nil {
		Logf("[Verify] Encryption type not found in spec")
		return "", false
	}

	// Check kubeapiserver for encryption status
	kubeAPIServerGVR := schema.GroupVersionResource{
		Group:    "operator.openshift.io",
		Version:  "v1",
		Resource: "kubeapiservers",
	}

	kubeAPIServer, err := client.Resource(kubeAPIServerGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		Logf("[Verify] Failed to get kubeapiserver: %v", err)
		return encType, false
	}

	// Get encryption conditions
	conditions, found, err := unstructured.NestedSlice(kubeAPIServer.Object, "status", "conditions")
	if !found || err != nil {
		Logf("[Verify] Conditions not found in kubeapiserver status")
		return encType, false
	}

	// Check for Encrypted condition
	encryptionCompleted := false
	for _, cond := range conditions {
		condition := cond.(map[string]interface{})
		if condition["type"] == "Encrypted" {
			reason, _ := condition["reason"].(string)
			if reason == "EncryptionCompleted" {
				encryptionCompleted = true
				message, _ := condition["message"].(string)
				Logf("[Verify] Encryption status: %s - %s", reason, message)
			}
		}
	}

	return encType, encryptionCompleted
}

// isFeatureGateEnabled checks if a specific feature gate is enabled
func isFeatureGateEnabled(ctx context.Context, client dynamic.Interface, featureName string) (bool, error) {
	featureGateGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "featuregates",
	}

	featureGate, err := client.Resource(featureGateGVR).Get(ctx, "cluster", v1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get feature gate: %w", err)
	}

	// Check status for enabled features
	featureGates, found, err := unstructured.NestedSlice(featureGate.Object, "status", "featureGates")
	if !found || err != nil {
		return false, nil
	}

	for _, fg := range featureGates {
		fgDetails := fg.(map[string]interface{})
		enabled, found, err := unstructured.NestedSlice(fgDetails, "enabled")
		if !found || err != nil {
			continue
		}

		for _, feature := range enabled {
			featureMap := feature.(map[string]interface{})
			if name, found := featureMap["name"]; found && name == featureName {
				return true, nil
			}
		}
	}

	return false, nil
}

// getStatusString returns a formatted status string
func getStatusString(enabled bool) string {
	if enabled {
		return "Already Enabled"
	}
	return "Newly Enabled   "
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		// Pad with spaces to maintain box alignment
		return fmt.Sprintf("%-"+fmt.Sprint(maxLen)+"s║", s)
	}
	return s[:maxLen-3] + "...║"
}

// debugNode executes a command on a node using oc debug with chroot
// Returns stdout, stderr, and error
func debugNode(nodeName string, cmd ...string) (string, string, error) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build the oc debug command arguments
	// oc debug node/<nodeName> -- chroot /host <cmd>
	args := []string{"debug", fmt.Sprintf("node/%s", nodeName), "--", "chroot", "/host"}
	args = append(args, cmd...)

	Logf("[DebugNode] Executing: oc %s", strings.Join(args, " "))

	command := exec.CommandContext(ctx, "oc", args...)

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	return stdout.String(), stderr.String(), err
}

// debugNodeRetryWithChroot executes a command on a node with retry logic
// Similar to compat_otp.DebugNodeRetryWithOptionsAndChroot
func debugNodeRetryWithChroot(nodeName string, cmd ...string) (string, error) {
	var stdErr string
	var stdOut string
	var err error

	// Retry logic with polling
	errWait := wait.Poll(3*time.Second, 30*time.Second, func() (bool, error) {
		stdOut, stdErr, err = debugNode(nodeName, cmd...)
		if err != nil {
			Logf("[DebugNode] Retry attempt failed: %v", err)
			return false, nil // Retry
		}
		return true, nil // Success
	})

	if errWait != nil {
		return "", fmt.Errorf("failed to debug node after retries: %w", errWait)
	}

	// Combine stdout and stderr
	return strings.Join([]string{stdOut, stdErr}, "\n"), err
}

// executeNodeCommand executes a command on a node using oc debug with chroot
func executeNodeCommand(nodeName, command string) (string, error) {
	Logf("[Exec] Running command on node %s", nodeName)
	Logf("[Exec] Command: %s", command)

	// Execute the command with retry
	output, err := debugNodeRetryWithChroot(nodeName, "/bin/bash", "-c", command)
	if err != nil {
		Logf("[Exec] Command failed: %v", err)
		return output, fmt.Errorf("failed to execute command on node %s: %w", nodeName, err)
	}

	Logf("[Exec] Command completed successfully")
	return output, nil
}

// getAwsCredentialFromCluster retrieves AWS credentials from the cluster's kube-system namespace
// and sets them as environment variables
func getAwsCredentialFromCluster() error {
	Logf("[AWS-Creds] Retrieving AWS credentials from cluster")

	// Get the aws-creds secret from kube-system namespace
	cmd := exec.Command("oc", "get", "secret/aws-creds", "-n", "kube-system", "-o", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Skip for STS and C2S clusters
		Logf("[AWS-Creds] Did not get credential to access AWS: %v", err)
		g.Skip("Did not get credential to access AWS, skip the testing.")
		return fmt.Errorf("failed to get AWS credentials from cluster: %w", err)
	}

	// Parse the JSON output
	var secret map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &secret); err != nil {
		return fmt.Errorf("failed to parse secret JSON: %w", err)
	}

	// Extract base64-encoded credentials
	data, ok := secret["data"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("secret data not found")
	}

	accessKeyIDBase64, ok1 := data["aws_access_key_id"].(string)
	secureKeyBase64, ok2 := data["aws_secret_access_key"].(string)
	if !ok1 || !ok2 {
		return fmt.Errorf("AWS credentials not found in secret")
	}

	// Decode base64 credentials
	accessKeyID, err := base64.StdEncoding.DecodeString(accessKeyIDBase64)
	if err != nil {
		return fmt.Errorf("failed to decode access key ID: %w", err)
	}

	secureKey, err := base64.StdEncoding.DecodeString(secureKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode secret access key: %w", err)
	}

	// Get AWS region from infrastructure resource
	cmd = exec.Command("oc", "get", "infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.aws.region}")
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to get AWS region: %w", err)
	}

	clusterRegion := strings.TrimSpace(stdout.String())

	// Set environment variables
	os.Setenv("AWS_ACCESS_KEY_ID", string(accessKeyID))
	os.Setenv("AWS_SECRET_ACCESS_KEY", string(secureKey))
	os.Setenv("AWS_REGION", clusterRegion)

	Logf("[AWS-Creds] ✓ AWS credentials set successfully")
	Logf("[AWS-Creds] Region: %s", clusterRegion)

	return nil
}
