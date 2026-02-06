package kms

import (
	"fmt"
	"regexp"
	"time"

	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const (
	DefaultEndpoint = "unix:///var/run/kmsplugin/kms.sock"
	DefaultTimeout  = 10 * time.Second
)

// providerNameRegex matches KMS provider names in format: kms-{resource}-{keyID}-{keySecret}
// Example: "kms-secrets-1-XUFAKrxLKna5cZnZEQH8Ug=="
var providerNameRegex = regexp.MustCompile(`^kms-(.+)-(\d+)-([A-Za-z0-9+/=]+)$`)

// ToProviderName converts resource name, key ID, and KMS secret to KMS provider name format: kms-{resourceName}-{keyID}-{keySecret}
// Example: "kms-secrets-1-XUFAKrxLKna5cZnZEQH8Ug=="
func ToProviderName(resourceName string, key apiserverconfigv1.Key) string {
	return fmt.Sprintf("kms-%s-%s-%s", resourceName, key.Name, key.Secret)
}

// FromProviderName extracts the key ID and KMS Secret from a KMS provider name.
// Expected format: kms-{resourceName}-{keyID}-{keySecret}
func FromProviderName(providerName string) (keyID string, kmsKey string, err error) {
	matches := providerNameRegex.FindStringSubmatch(providerName)
	if len(matches) != 4 {
		return "", "", fmt.Errorf("provider name %q has invalid format, expected kms-{resource}-{keyID}-{checksumBase64}", providerName)
	}

	return matches[2], matches[3], nil
}
