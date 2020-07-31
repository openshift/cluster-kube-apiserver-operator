package audit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	auditv1beta1 "k8s.io/apiserver/pkg/apis/audit/v1beta1"
)

func TestEnsureAuditPolicies(t *testing.T) {
	tests := []struct {
		name               string
		expectedPolicyName string
	}{
		{
			name:               "WithDefault",
			expectedPolicyName: "Default",
		},
		{
			name:               "WithWriteRequestBodies",
			expectedPolicyName: "WriteRequestBodies",
		},
		{
			name:               "WithAllRequestBodies",
			expectedPolicyName: "AllRequestBodies",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := fmt.Sprintf("%s.yaml", strings.ToLower(test.expectedPolicyName))
			raw, err := getPolicyFromResource(key)
			require.NoError(t, err)
			require.NotNil(t, raw)

			policyGot := auditv1beta1.Policy{}
			err = json.Unmarshal(raw, &policyGot)
			require.NoError(t, err)
			require.Equal(t, test.expectedPolicyName, policyGot.GetName())
		})
	}
}

func TestAuditPolicyPathGetter(t *testing.T) {
	tests := []struct {
		name         string
		profile      string
		expectedPath string
		errExpected  bool
	}{
		{
			name:         "WithDefault",
			profile:      "Default",
			expectedPath: "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-audit-policies/default.yaml",
		},
		{
			name:         "WithWriteRequestBodies",
			profile:      "WriteRequestBodies",
			expectedPath: "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-audit-policies/writerequestbodies.yaml",
		},
		{
			name:         "WithAllRequestBodies",
			profile:      "AllRequestBodies",
			expectedPath: "/etc/kubernetes/static-pod-resources/configmaps/kube-apiserver-audit-policies/allrequestbodies.yaml",
		},
		{
			name:        "WithNonExistentPolicy",
			profile:     "Foo",
			errExpected: true,
		},
	}

	pathGetter, err := NewAuditPolicyPathGetter()
	require.NoError(t, err)
	require.NotNil(t, pathGetter)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pathGot, err := pathGetter(test.profile)

			if test.errExpected {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.expectedPath, pathGot)
			}
		})
	}
}
