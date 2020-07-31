package audit

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"
	libgoapiserver "github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
)

const (
	policyAsset = "v4.1.0/kube-apiserver/audit-policies-cm.yaml"
)

func NewAuditPolicyPathGetter() (libgoapiserver.AuditPolicyPathGetterFunc, error) {
	policies, err := readPolicyNamesFromResource()
	if err != nil {
		return nil, err
	}

	return func(profile string) (string, error) {
		const (
			auditPolicyFilePath = "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-audit-policies"
		)

		// we expect the keys for audit profile in bindata to be in lower case and
		// have a '.yaml' suffix.
		key := fmt.Sprintf("%s.yaml", strings.ToLower(profile))
		_, exists := policies[key]
		if !exists {
			return "", fmt.Errorf("invalid audit profile - key=%s", key)
		}

		return fmt.Sprintf("%s/%s", auditPolicyFilePath, key), nil
	}, nil
}

// DefaultPolicy returns the default audit policy.
func DefaultPolicy() ([]byte, error) {
	return getPolicyFromResource("default.yaml")
}

func readPolicyNamesFromResource() (map[string]struct{}, error) {
	bytes, err := v410_00_assets.Asset(policyAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to load asset name=%s - %s", policyAsset, err)
	}

	rawJSON, err := kyaml.ToJSON(bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset yaml to JSON name=%s - %s", policyAsset, err)
	}

	cm := corev1.ConfigMap{}
	if err := json.Unmarshal(rawJSON, &cm); err != nil {
		return nil, fmt.Errorf("failed to unmarshal audit policy profile=%s - %s", policyAsset, err)
	}

	policies := map[string]struct{}{}
	for key := range cm.Data {
		policies[key] = struct{}{}
	}

	return policies, nil
}

func getPolicyFromResource(key string) ([]byte, error) {
	bytes, err := v410_00_assets.Asset(policyAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to load asset name=%s - %s", policyAsset, err)
	}

	rawJSON, err := kyaml.ToJSON(bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset yaml to JSON name=%s - %s", policyAsset, err)
	}

	cm := corev1.ConfigMap{}
	if err := json.Unmarshal(rawJSON, &cm); err != nil {
		return nil, fmt.Errorf("failed to unmarshal audit policy asset name=%s - %s", policyAsset, err)
	}

	value, ok := cm.Data[key]
	if !ok || len(value) == 0 {
		return nil, fmt.Errorf("policy not found key=%s asset=%s", key, policyAsset)
	}

	raw, err := kyaml.ToJSON([]byte(value))
	if err != nil {
		return nil, fmt.Errorf("failed to convert audit policy yaml to JSON key=%s - %s", key, err)
	}

	return raw, nil
}
