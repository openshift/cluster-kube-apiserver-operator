package extended

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmsTypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// awsInstance implements ComputeNode interface for AWS platform
type awsInstance struct {
	instance
	awsConfig aws.Config
	region    string
}

// GetAwsNodes gets AWS nodes and loads cloud credentials with the specified label
func GetAwsNodes(ctx context.Context, kubeClient *kubernetes.Clientset, dynamicClient dynamic.Interface, label string) ([]ComputeNode, func()) {
	// Get AWS credentials from cluster
	err := getAwsCredentialFromCluster()
	o.Expect(err).NotTo(o.HaveOccurred())

	region := getAWSRegion()

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	o.Expect(err).NotTo(o.HaveOccurred())

	// Get node names
	nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("node-role.kubernetes.io/%s", label),
	})
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(len(nodeList.Items)).To(o.BeNumerically(">", 0), "No nodes found with label %s", label)

	var results []ComputeNode
	for _, node := range nodeList.Items {
		results = append(results, newAwsInstance(ctx, kubeClient, dynamicClient, node.Name, cfg, region))
	}

	return results, nil
}

func newAwsInstance(ctx context.Context, kubeClient *kubernetes.Clientset, dynamicClient dynamic.Interface, nodeName string, awsConfig aws.Config, region string) *awsInstance {
	return &awsInstance{
		instance: instance{
			nodeName:      nodeName,
			kubeClient:    kubeClient,
			dynamicClient: dynamicClient,
			ctx:           ctx,
		},
		awsConfig: awsConfig,
		region:    region,
	}
}

// GetInstanceID retrieves the EC2 instance ID from the node's provider ID
func (a *awsInstance) GetInstanceID() (string, error) {
	node, err := a.kubeClient.CoreV1().Nodes().Get(a.ctx, a.nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %w", a.nodeName, err)
	}

	// Provider ID format: aws:///<availability-zone>/<instance-id>
	// Example: aws:///us-east-1a/i-1234567890abcdef0
	providerID := node.Spec.ProviderID
	if providerID == "" {
		return "", fmt.Errorf("node %s has no provider ID", a.nodeName)
	}

	parts := strings.Split(providerID, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid provider ID format: %s", providerID)
	}

	instanceID := parts[len(parts)-1]
	return instanceID, nil
}

// CreateKMSKey creates or retrieves an AWS KMS key for testing
func (a *awsInstance) CreateKMSKey() string {
	Logf("[AWS-KMS] Initializing AWS KMS client in region: %s", a.region)
	kmsClient := kms.NewFromConfig(a.awsConfig)
	rgtClient := resourcegroupstaggingapi.NewFromConfig(a.awsConfig)

	// Check for existing test keys with the specific tag
	Logf("[AWS-KMS] Searching for existing KMS keys with tag Purpose=ocp-kms-qe-ci-test")
	getResourcesInput := &resourcegroupstaggingapi.GetResourcesInput{
		ResourceTypeFilters: []string{"kms"},
		TagFilters: []rgttypes.TagFilter{
			{
				Key:    aws.String("Purpose"),
				Values: []string{"ocp-kms-qe-ci-test"},
			},
		},
	}

	existingKeys, err := rgtClient.GetResources(a.ctx, getResourcesInput)
	o.Expect(err).NotTo(o.HaveOccurred())

	var myKmsKeyArn string

	if len(existingKeys.ResourceTagMappingList) > 0 {
		myKmsKeyArn = *existingKeys.ResourceTagMappingList[0].ResourceARN
		Logf("[AWS-KMS] Found existing KMS key: %s", myKmsKeyArn)
		g.By(fmt.Sprintf("Found existing KMS key: %s", myKmsKeyArn))

		// Check if key is scheduled for deletion and cancel if needed
		Logf("[AWS-KMS] Checking key status for: %s", myKmsKeyArn)
		describeInput := &kms.DescribeKeyInput{
			KeyId: aws.String(myKmsKeyArn),
		}
		keyMetadata, err := kmsClient.DescribeKey(a.ctx, describeInput)
		o.Expect(err).NotTo(o.HaveOccurred())

		Logf("[AWS-KMS] Key state: %s", keyMetadata.KeyMetadata.KeyState)
		if keyMetadata.KeyMetadata.DeletionDate != nil {
			Logf("[AWS-KMS] Key is scheduled for deletion on: %v", keyMetadata.KeyMetadata.DeletionDate)
			g.By("Canceling scheduled deletion and enabling key")

			Logf("[AWS-KMS] Canceling key deletion...")
			_, err = kmsClient.CancelKeyDeletion(a.ctx, &kms.CancelKeyDeletionInput{
				KeyId: aws.String(myKmsKeyArn),
			})
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[AWS-KMS] ✓ Deletion canceled")

			Logf("[AWS-KMS] Enabling key...")
			_, err = kmsClient.EnableKey(a.ctx, &kms.EnableKeyInput{
				KeyId: aws.String(myKmsKeyArn),
			})
			o.Expect(err).NotTo(o.HaveOccurred())
			Logf("[AWS-KMS] ✓ Key enabled")
		} else {
			Logf("[AWS-KMS] Key is active and ready to use")
		}
	} else {
		Logf("[AWS-KMS] No existing key found, creating new KMS key")
		g.By("Creating new KMS key")
		createKeyInput := &kms.CreateKeyInput{
			Description: aws.String("OCP KMS QE CI Test Key"),
			KeySpec:     kmsTypes.KeySpecSymmetricDefault,
			KeyUsage:    kmsTypes.KeyUsageTypeEncryptDecrypt,
			Tags: []kmsTypes.Tag{
				{
					TagKey:   aws.String("Purpose"),
					TagValue: aws.String("ocp-kms-qe-ci-test"),
				},
			},
		}

		Logf("[AWS-KMS] Creating KMS key with spec: SYMMETRIC_DEFAULT, usage: ENCRYPT_DECRYPT")
		createResult, err := kmsClient.CreateKey(a.ctx, createKeyInput)
		if err != nil {
			if strings.Contains(err.Error(), "AccessDeniedException") {
				Logf("[AWS-KMS] ✗ Access denied - insufficient permissions")
				g.Skip("AWS credentials don't have permission to create KMS keys")
			}
			Logf("[AWS-KMS] ✗ Failed to create key: %v", err)
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		myKmsKeyArn = *createResult.KeyMetadata.Arn
		Logf("[AWS-KMS] ✓ Created new KMS key: %s", myKmsKeyArn)
		Logf("[AWS-KMS]   Key ID: %s", *createResult.KeyMetadata.KeyId)
		g.By(fmt.Sprintf("Created KMS key: %s", myKmsKeyArn))
	}

	return myKmsKeyArn
}

// DeleteKMSKey schedules a KMS key for deletion
func (a *awsInstance) DeleteKMSKey(keyArn string) {
	Logf("[AWS-KMS] Scheduling KMS key for deletion: %s", keyArn)
	kmsClient := kms.NewFromConfig(a.awsConfig)

	// Schedule key deletion with minimum waiting period (7 days)
	input := &kms.ScheduleKeyDeletionInput{
		KeyId:               aws.String(keyArn),
		PendingWindowInDays: aws.Int32(7), // Minimum allowed by AWS
	}

	result, err := kmsClient.ScheduleKeyDeletion(a.ctx, input)
	if err != nil {
		// Don't fail the test if key deletion fails
		Logf("[AWS-KMS] Warning: Failed to schedule key deletion: %v", err)
		return
	}

	if result.DeletionDate != nil {
		Logf("[AWS-KMS] ✓ Key scheduled for deletion on: %v", *result.DeletionDate)
	} else {
		Logf("[AWS-KMS] ✓ Key deletion scheduled")
	}
	g.By(fmt.Sprintf("Scheduled KMS key deletion: %s", keyArn))
}

// LoadKMSTestCasesFromYAML loads test cases from the YAML file
func (a *awsInstance) LoadKMSTestCasesFromYAML() ([]YamlKmsTestCase, error) {
	testDataFile := filepath.Join("testdata", "kms_tests_aws.yaml")

	data, err := os.ReadFile(testDataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read test data file: %w", err)
	}

	var testCases []YamlKmsTestCase
	err = yaml.Unmarshal(data, &testCases)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal test cases: %w", err)
	}

	return testCases, nil
}

// GetIamRoleNameFromId retrieves the IAM role name attached to the EC2 instance
func (a *awsInstance) GetIamRoleNameFromId() string {
	Logf("[AWS-IAM] Retrieving IAM role for instance: %s", a.nodeName)
	instanceID, err := a.GetInstanceID()
	o.Expect(err).NotTo(o.HaveOccurred())
	Logf("[AWS-IAM] Instance ID: %s", instanceID)

	ec2Client := ec2.NewFromConfig(a.awsConfig)
	iamClient := iam.NewFromConfig(a.awsConfig)

	// Describe the instance to get IAM instance profile
	Logf("[AWS-IAM] Describing EC2 instance...")
	describeInput := &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}

	result, err := ec2Client.DescribeInstances(a.ctx, describeInput)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(len(result.Reservations)).To(o.BeNumerically(">", 0))
	o.Expect(len(result.Reservations[0].Instances)).To(o.BeNumerically(">", 0))

	instance := result.Reservations[0].Instances[0]
	o.Expect(instance.IamInstanceProfile).NotTo(o.BeNil())
	o.Expect(instance.IamInstanceProfile.Arn).NotTo(o.BeNil())

	Logf("[AWS-IAM] Instance profile ARN: %s", *instance.IamInstanceProfile.Arn)

	// Extract profile name from ARN
	arnParts := strings.Split(*instance.IamInstanceProfile.Arn, "/")
	o.Expect(len(arnParts)).To(o.BeNumerically(">=", 2))
	profileName := arnParts[1]
	Logf("[AWS-IAM] Instance profile name: %s", profileName)

	// Get instance profile to retrieve role
	Logf("[AWS-IAM] Fetching instance profile details...")
	profileInput := &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	}

	profileOutput, err := iamClient.GetInstanceProfile(a.ctx, profileInput)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(len(profileOutput.InstanceProfile.Roles)).To(o.BeNumerically(">", 0))

	roleName := *profileOutput.InstanceProfile.Roles[0].RoleName
	Logf("[AWS-IAM] ✓ IAM role name: %s", roleName)
	g.By(fmt.Sprintf("IAM Role Name for instance %s: %s", instanceID, roleName))

	return roleName
}

const keyAWSPolicyTemplate = `
{
  "Id": "key-policy-01",
  "Statement": [
    {
      "Sid": "Enable IAM User Permissions",
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::{{.AccountID}}:root"
      },
      "Action": "kms:*",
      "Resource": "*"
    },
    {
      "Sid": "Allow use of the key",
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::{{.AccountID}}:role/{{.MasterRoleName}}"
      },
      "Action": [
        "kms:Encrypt",
        "kms:Decrypt",
        "kms:ReEncrypt*",
        "kms:GenerateDataKey*",
        "kms:DescribeKey"
      ],
      "Resource": "*"
    },
    {
      "Sid": "Allow attachment of persistent resources",
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::{{.AccountID}}:role/{{.MasterRoleName}}"
      },
      "Action": [
        "kms:CreateGrant",
        "kms:ListGrants",
        "kms:RevokeGrant"
      ],
      "Resource": "*",
      "Condition": {
        "Bool": {
          "kms:GrantIsForAWSResource": "true"
        }
      }
    }
  ]
}
`

type KeyAWSPolicyData struct {
	AccountID      string
	MasterRoleName string
}

// RenderKmsKeyPolicy renders the KMS key policy template
func (a *awsInstance) RenderKmsKeyPolicy() string {
	Logf("[AWS-Policy] Rendering KMS key policy template")
	stsClient := sts.NewFromConfig(a.awsConfig)

	// Get AWS account ID
	Logf("[AWS-Policy] Retrieving AWS account ID via STS...")
	callerIdentity, err := stsClient.GetCallerIdentity(a.ctx, &sts.GetCallerIdentityInput{})
	o.Expect(err).NotTo(o.HaveOccurred())

	accountID := *callerIdentity.Account
	Logf("[AWS-Policy] AWS Account ID: %s", accountID)

	masterRoleName := a.GetIamRoleNameFromId()

	Logf("[AWS-Policy] Parsing policy template...")
	tmpl, err := template.New("keyPolicy").Parse(keyAWSPolicyTemplate)
	o.Expect(err).NotTo(o.HaveOccurred())

	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, KeyAWSPolicyData{
		AccountID:      accountID,
		MasterRoleName: masterRoleName,
	})
	o.Expect(err).NotTo(o.HaveOccurred())

	Logf("[AWS-Policy] ✓ Policy rendered for account %s and role %s", accountID, masterRoleName)
	g.By(fmt.Sprintf("Rendered KMS Policy for account %s and role %s", accountID, masterRoleName))
	return rendered.String()
}

// UpdateKmsPolicy updates the KMS key policy
func (a *awsInstance) UpdateKmsPolicy(keyID string) {
	Logf("[AWS-KMS] Updating KMS key policy for: %s", keyID)
	kmsClient := kms.NewFromConfig(a.awsConfig)
	kmsPolicy := a.RenderKmsKeyPolicy()

	Logf("[AWS-KMS] Applying policy to key...")
	putPolicyInput := &kms.PutKeyPolicyInput{
		KeyId:      aws.String(keyID),
		PolicyName: aws.String("default"),
		Policy:     aws.String(kmsPolicy),
	}

	_, err := kmsClient.PutKeyPolicy(a.ctx, putPolicyInput)
	if err != nil {
		Logf("[AWS-KMS] ✗ Failed to update policy: %v", err)
	}
	o.Expect(err).NotTo(o.HaveOccurred())

	Logf("[AWS-KMS] ✓ Policy updated successfully")
	g.By(fmt.Sprintf("Updated KMS key policy for key: %s", keyID))
}

// GetRegionFromARN extracts the region from an AWS KMS ARN
// ARN format: arn:aws:kms:region:account:key/key-id
func (a *awsInstance) GetRegionFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 4 {
		Logf("[AWS] Warning: Invalid ARN format: %s", arn)
		return a.region // fallback to instance region
	}
	return parts[3]
}

// VerifyEncryptionType calls the generic utility function
func (a *awsInstance) VerifyEncryptionType(ctx context.Context, client dynamic.Interface) (string, bool) {
	return verifyEncryptionType(ctx, client)
}

// VerifySecretEncryption verifies that a secret is encrypted in etcd with the expected format
// Returns: (isEncrypted, encryptionFormat)
func (a *awsInstance) VerifySecretEncryption(ctx context.Context, namespace, secretName string) (bool, string) {
	Logf("[Verify-Secret] Checking encryption for secret %s/%s", namespace, secretName)

	// Execute etcdctl command to get the secret from etcd
	etcdKey := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", namespace, secretName)

	// Use single quotes around the etcd key to prevent shell expansion
	command := fmt.Sprintf(
		"ETCD_POD=$(sudo crictl ps --name=etcd-member -q) && sudo crictl exec $ETCD_POD etcdctl get '%s' --prefix --keys-only",
		etcdKey,
	)

	output, err := a.ExecuteCommand(command)
	if err != nil {
		Logf("[Verify-Secret] Failed to query etcd: %v", err)
		return false, ""
	}

	// Check if key exists
	if !strings.Contains(output, etcdKey) {
		Logf("[Verify-Secret] Secret not found in etcd")
		return false, ""
	}

	// Get the actual value to check encryption format
	command = fmt.Sprintf(
		"ETCD_POD=$(sudo crictl ps --name=etcd-member -q) && sudo crictl exec $ETCD_POD etcdctl get '%s' --print-value-only | head -c 20",
		etcdKey,
	)

	value, err := a.ExecuteCommand(command)
	if err != nil {
		Logf("[Verify-Secret] Failed to get secret value from etcd: %v", err)
		return false, ""
	}

	// Check for KMSv2 encryption prefix
	if strings.HasPrefix(value, "k8s:enc:kms:v2:") {
		Logf("[Verify-Secret] ✓ Secret is encrypted with KMSv2")
		return true, "k8s:enc:kms:v2:"
	} else if strings.HasPrefix(value, "k8s:enc:kms:v1:") {
		Logf("[Verify-Secret] Secret is encrypted with KMSv1")
		return true, "k8s:enc:kms:v1:"
	} else if strings.HasPrefix(value, "k8s:enc:") {
		Logf("[Verify-Secret] Secret is encrypted with format: %s", value[:15])
		return true, value[:15]
	}

	Logf("[Verify-Secret] Secret is not encrypted (no k8s:enc: prefix)")
	return false, ""
}

// VerifyOAuthTokenEncryption verifies that an OAuth token is encrypted in etcd
// Returns: (isEncrypted, encryptionFormat)
func (a *awsInstance) VerifyOAuthTokenEncryption(ctx context.Context, tokenType, tokenName string) (bool, string) {
	Logf("[Verify-OAuth] Checking encryption for %s: %s", tokenType, tokenName)

	// etcd key format for OAuth tokens
	var etcdKey string
	if tokenType == "oauthaccesstokens" {
		etcdKey = fmt.Sprintf("/kubernetes.io/oauth.openshift.io/oauthaccesstokens/%s", tokenName)
	} else if tokenType == "oauthauthorizetokens" {
		etcdKey = fmt.Sprintf("/kubernetes.io/oauth.openshift.io/oauthauthorizetokens/%s", tokenName)
	} else {
		Logf("[Verify-OAuth] Unknown token type: %s", tokenType)
		return false, ""
	}

	// Use single quotes around the etcd key to prevent shell expansion of special chars like ~
	command := fmt.Sprintf(
		"ETCD_POD=$(sudo crictl ps --name=etcd-member -q) && sudo crictl exec $ETCD_POD etcdctl get '%s' --prefix --keys-only",
		etcdKey,
	)

	output, err := a.ExecuteCommand(command)
	if err != nil {
		Logf("[Verify-OAuth] Failed to query etcd: %v", err)
		return false, ""
	}

	// Check if key exists
	if !strings.Contains(output, etcdKey) {
		Logf("[Verify-OAuth] Token not found in etcd")
		return false, ""
	}

	// Get the actual value to check encryption format
	command = fmt.Sprintf(
		"ETCD_POD=$(sudo crictl ps --name=etcd-member -q) && sudo crictl exec $ETCD_POD etcdctl get '%s' --print-value-only | head -c 20",
		etcdKey,
	)

	value, err := a.ExecuteCommand(command)
	if err != nil {
		Logf("[Verify-OAuth] Failed to get token value from etcd: %v", err)
		return false, ""
	}

	// Check for KMSv2 encryption prefix
	if strings.HasPrefix(value, "k8s:enc:kms:v2:") {
		Logf("[Verify-OAuth] ✓ OAuth token is encrypted with KMSv2")
		return true, "k8s:enc:kms:v2:"
	} else if strings.HasPrefix(value, "k8s:enc:kms:v1:") {
		Logf("[Verify-OAuth] OAuth token is encrypted with KMSv1")
		return true, "k8s:enc:kms:v1:"
	} else if strings.HasPrefix(value, "k8s:enc:") {
		Logf("[Verify-OAuth] OAuth token is encrypted with format: %s", value[:15])
		return true, value[:15]
	}

	Logf("[Verify-OAuth] OAuth token is not encrypted (no k8s:enc: prefix)")
	return false, ""
}
