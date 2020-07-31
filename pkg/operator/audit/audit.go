package audit

import (
	"fmt"

	kyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	libgoassets "github.com/openshift/library-go/pkg/operator/apiserver/audit"
)

// DefaultPolicy returns the default audit policy.
func DefaultPolicy() ([]byte, error) {
	return getPolicyFromResource("kube-apiserver-audit-policies", operatorclient.TargetNamespace, "default.yaml")
}

func getPolicyFromResource(targetName, targetNamespace, targetKey string) ([]byte, error) {
	cm, err := libgoassets.GetAuditPolicies(targetName, targetNamespace)
	if err != nil {
		return nil, err
	}

	value, ok := cm.Data[targetKey]
	if !ok || len(value) == 0 {
		return nil, fmt.Errorf("policy not found for key=%s ", targetKey)
	}

	raw, err := kyaml.ToJSON([]byte(value))
	if err != nil {
		return nil, fmt.Errorf("failed to convert audit policy yaml to JSON key=%s - %s", targetKey, err)
	}

	return raw, nil
}
