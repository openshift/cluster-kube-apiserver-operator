package kubeletversionskewcontroller

import (
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func Test_kubeletVersionSkewController_Sync(t *testing.T) {

	evenOpenShiftVersion := "4.8.0"
	oddOpenShiftVersion := "4.9.0"
	apiServerVersion := "1.21.1"
	skewedKubeletVersions := func(s ...int) []string {
		var v []string
		for i, skew := range s {
			v = append(v, fmt.Sprintf("1.%d.%d", 21+skew, i))
		}
		return v
	}

	testCases := []struct {
		name             string
		ocpVersion       string
		kubeletVersions  []string
		expectedStatus   operatorv1.ConditionStatus
		expectedReason   string
		expectedMsgLines string
	}{
		{
			name:             "Synced/Even",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSyncedReason,
			expectedMsgLines: "Kubelet and API server minor versions are synced.",
		},
		{
			name:             "Synced/Odd",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSyncedReason,
			expectedMsgLines: "Kubelet and API server minor versions are synced.",
		},
		{
			name:             "ErrorParsingKubeletVersion",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  []string{"Invalid", "1.21.2", "1.20.3"},
			expectedStatus:   operatorv1.ConditionUnknown,
			expectedReason:   KubeletVersionUnknownReason,
			expectedMsgLines: "Unable to determine the kubelet version on node test000: No Major.Minor.Patch elements found",
		},
		{
			name:             "UnsupportedNextUpgrade/Even",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -1, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet minor version (1.20.1) on node test001 will not be supported in the next OpenShift minor version upgrade.",
		},
		{
			name:             "UnsupportedNextUpgrade/Odd",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -2, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported kubelet minor version (1.19.1) on node test001 is too far behind the target API server version (1.21.1).",
		},
		{
			name:             "TwoNodesNotSynced",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -1, -1),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet minor versions on nodes test001 and test002 will not be supported in the next OpenShift minor version upgrade.",
		},
		{
			name:             "ThreeNodesNotSynced",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -1, -1, -1),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet minor versions on nodes test001, test002, and test003 will not be supported in the next OpenShift minor version upgrade.",
		},
		{
			name:             "ManyNodesNotSynced",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -1, -1, -1, -1, -1, 0, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet minor versions on 5 nodes will not be supported in the next OpenShift minor version upgrade.",
		},
		{
			name:             "SkewedUnsupported/Even",
			ocpVersion:       evenOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -3, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported kubelet minor version (1.18.1) on node test001 is too far behind the target API server version (1.21.1).",
		},
		{
			name:             "SkewedUnsupported/Odd",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -2, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported kubelet minor version (1.19.1) on node test001 is too far behind the target API server version (1.21.1).",
		},
		{
			name:             "SkewedButOK/Odd",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(-1, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet minor version (1.20.0) on node test000 is behind the expected API server version; nevertheless, it will continue to be supported in the next OpenShift minor version upgrade.",
		},
		{
			name:             "Unsupported",
			ocpVersion:       oddOpenShiftVersion,
			kubeletVersions:  skewedKubeletVersions(0, -1, 1),
			expectedStatus:   operatorv1.ConditionUnknown,
			expectedReason:   KubeletMinorVersionAheadReason,
			expectedMsgLines: "Unsupported kubelet minor version (1.22.2) on node test002 is ahead of the target API server version (1.21.1).",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for i, kv := range tc.kubeletVersions {
				indexer.Add(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("test%03d", i)},
					Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: kv}},
				})
			}
			status := &operatorv1.StaticPodOperatorStatus{}
			ocpVersion := semver.MustParse(tc.ocpVersion)
			nextOpenShiftVersion := semver.Version{Major: ocpVersion.Major, Minor: ocpVersion.Minor + 1}
			c := &kubeletVersionSkewController{
				operatorClient: v1helpers.NewFakeStaticPodOperatorClient(
					&operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}},
					status, nil, nil,
				),
				nodeLister:                  corev1listers.NewNodeLister(indexer),
				apiServerVersion:            semver.MustParse(apiServerVersion),
				minSupportedSkew:            minSupportedKubeletSkewForOpenShiftVersion(ocpVersion),
				minSupportedSkewNextVersion: minSupportedKubeletSkewForOpenShiftVersion(nextOpenShiftVersion),
			}
			err := c.sync(nil, nil)
			if err != nil {
				t.Fatalf("sync() unexpected err: %v", err)
			}
			if len(status.Conditions) != 1 || status.Conditions[0].Type != KubeletMinorVersionUpgradeableConditionType {
				t.Errorf("Expected %s condition type.", KubeletMinorVersionUpgradeableConditionType)
			}
			condition := status.Conditions[0]
			if tc.expectedStatus != condition.Status {
				t.Errorf("Condition status: expected %s, actual %s", tc.expectedStatus, condition.Status)
			}
			if tc.expectedReason != condition.Reason {
				t.Errorf("Condition reason: expected %s, actual %s", tc.expectedReason, condition.Reason)
			}
			if tc.expectedMsgLines != condition.Message {
				t.Errorf("Expected condition message to match %q.", tc.expectedMsgLines)
				t.Log(diff.StringDiff(tc.expectedMsgLines, condition.Message))
			}
			if t.Failed() {
				t.Logf(condition.Message)
			}
		})
	}
}
