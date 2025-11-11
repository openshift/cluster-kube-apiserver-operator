package extended

import (
	"context"
	"fmt"
	"os"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// YamlKmsTestCase represents a KMS test case from YAML
type YamlKmsTestCase struct {
	Name          string `yaml:"name"`
	Initial       string `yaml:"initial"`
	Expected      string `yaml:"expected,omitempty"`
	ExpectedError string `yaml:"expectedError,omitempty"`
}

// ComputeNode interface to handle compute nodes across different cloud platforms
type ComputeNode interface {
	GetName() string
	GetInstanceID() (string, error)
	CreateKMSKey() string
	DeleteKMSKey(keyArn string)
	LoadKMSTestCasesFromYAML() ([]YamlKmsTestCase, error)
	GetIamRoleNameFromId() string
	RenderKmsKeyPolicy() string
	UpdateKmsPolicy(keyID string)
	GetRegionFromARN(arn string) string
	VerifyEncryptionType(ctx context.Context, client dynamic.Interface) (string, bool)
	VerifySecretEncryption(ctx context.Context, namespace, secretName string) (bool, string)
	VerifyOAuthTokenEncryption(ctx context.Context, tokenType, tokenName string) (bool, string)
	ExecuteCommand(command string) (string, error)
}

// instance is the base struct for all compute node implementations
type instance struct {
	nodeName      string
	kubeClient    *kubernetes.Clientset
	dynamicClient dynamic.Interface
	ctx           context.Context
}

func (i *instance) GetName() string {
	return i.nodeName
}

// ExecuteCommand executes a command on the node via oc debug
func (i *instance) ExecuteCommand(command string) (string, error) {
	// Use the executeNodeCommand wrapper from util.go
	return executeNodeCommand(i.nodeName, command)
}

// ComputeNodes handles a collection of ComputeNode interfaces
type ComputeNodes []ComputeNode

// GetNodes gets master nodes according to platform with the specified label
func GetNodes(ctx context.Context, kubeClient *kubernetes.Clientset, dynamicClient dynamic.Interface, label string) (ComputeNodes, func()) {
	platform := checkPlatform(kubeClient)

	switch platform {
	case "aws":
		return GetAwsNodes(ctx, kubeClient, dynamicClient, label)
	case "gcp":
		g.Skip("GCP platform KMS support not yet implemented")
		return nil, nil
	case "azure":
		g.Skip("Azure platform KMS support not yet implemented")
		return nil, nil
	default:
		g.Skip(fmt.Sprintf("Platform %s is not supported for KMS tests. Expected AWS, GCP, or Azure.", platform))
		return nil, nil
	}
}

// checkPlatform determines the cloud platform of the cluster
func checkPlatform(kubeClient *kubernetes.Clientset) string {
	// Check for AWS-specific labels or annotations
	nodes, err := kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		return "unknown"
	}

	node := nodes.Items[0]

	// Check provider ID format
	if providerID := node.Spec.ProviderID; providerID != "" {
		if strings.HasPrefix(providerID, "aws://") {
			return "aws"
		}
		if strings.HasPrefix(providerID, "gce://") {
			return "gcp"
		}
		if strings.HasPrefix(providerID, "azure://") {
			return "azure"
		}
	}

	return "unknown"
}

// getAWSRegion gets the AWS region from environment or config
func getAWSRegion() string {
	if region := os.Getenv("AWS_REGION"); region != "" {
		return region
	}
	// Default to us-east-1 if not specified
	return "us-east-1"
}
