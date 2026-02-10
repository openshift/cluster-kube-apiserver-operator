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
	minorZeroOCPVersion := "5.0.0"
	minorZeroKubeApiVersion := "1.36.0"

	minorOneOCPVersion := "5.1.0"
	minorOneKubeApiVersion := "1.37.0"

	minorTwoOCPVersion := "5.2.0"
	minorTwoKubeApiVersion := "1.38.0"

	skewedKubeletVersions := func(base string, s ...int) []string {
		bb := semver.MustParse(base)
		var v []string
		for _, skew := range s {
			v = append(v, semver.Version{Major: bb.Major, Minor: bb.Minor + uint64(skew)}.FinalizeVersion())
		}
		return v
	}

	testCases := []struct {
		name             string
		ocpVersion       string
		apiServerVersion string
		kubeletVersions  []string
		expectedStatus   operatorv1.ConditionStatus
		expectedReason   string
		expectedMsgLines string
	}{
		{
			name:             "Synced/Zero",
			ocpVersion:       minorZeroOCPVersion,
			apiServerVersion: minorZeroKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorZeroKubeApiVersion, 0, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSyncedReason,
			expectedMsgLines: "Kubelet and API server versions are synced.",
		},
		{
			name:             "Synced/One",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, 0, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSyncedReason,
			expectedMsgLines: "Kubelet and API server versions are synced.",
		},
		{
			name:             "Synced/Two",
			ocpVersion:       minorTwoOCPVersion,
			apiServerVersion: minorTwoKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorTwoKubeApiVersion, 0, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSyncedReason,
			expectedMsgLines: "Kubelet and API server versions are synced.",
		},
		{
			name:             "ErrorParsingKubeletVersion",
			ocpVersion:       minorZeroOCPVersion,
			apiServerVersion: minorZeroKubeApiVersion,
			kubeletVersions:  []string{"Invalid", minorZeroKubeApiVersion, minorZeroKubeApiVersion},
			expectedStatus:   operatorv1.ConditionUnknown,
			expectedReason:   KubeletVersionUnknownReason,
			expectedMsgLines: "Unable to determine the kubelet version on node test000: No Major.Minor.Patch elements found",
		},
		{
			name:             "SkewedButOK",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, -1, 0, 0),
			expectedStatus:   operatorv1.ConditionTrue,
			expectedReason:   KubeletMinorVersionSupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet version (1.36.0) on node test000 is behind the expected API server version; nevertheless, it will continue to be supported in the next OpenShift version upgrade.",
		},
		{
			name:             "UnsupportedThisUpgrade",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, 0, -3, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported Kubelet version (1.34.0) on node test001 is too far behind the target API server version (1.37.0).",
		},
		{
			name:             "UnsupportedTwoNodes",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, -3, 0, -3),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported Kubelet versions on nodes test000 and test002 are too far behind the target API server version (1.37.0).",
		},
		{
			name:             "UnsupportedAllNodes",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, -3, -3, -3),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedReason,
			expectedMsgLines: "Unsupported Kubelet versions on nodes test000, test001, and test002 are too far behind the target API server version (1.37.0).",
		},
		{
			name:             "UnsupportedNextUpgrade",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, 0, -2, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet version (1.35.0) on node test001 will not be supported in the next OpenShift version upgrade.",
		},
		{
			name:             "UnsupportedNextUpgradeEUS",
			ocpVersion:       minorTwoOCPVersion,
			apiServerVersion: minorTwoKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorTwoKubeApiVersion, 0, -2, 0),
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReason:   KubeletMinorVersionUnsupportedNextUpgradeReason,
			expectedMsgLines: "Kubelet version (1.36.0) on node test001 will not be supported in the next OpenShift version upgrade.",
		},
		{
			name:             "UnsupportedAhead",
			ocpVersion:       minorOneOCPVersion,
			apiServerVersion: minorOneKubeApiVersion,
			kubeletVersions:  skewedKubeletVersions(minorOneKubeApiVersion, 0, -1, 1),
			expectedStatus:   operatorv1.ConditionUnknown,
			expectedReason:   KubeletMinorVersionAheadReason,
			expectedMsgLines: "Unsupported Kubelet version (1.38.0) on node test002 is ahead of the target API server version (1.37.0).",
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
			apiServerVersion := semver.MustParse(tc.apiServerVersion)
			nextOpenShiftVersion := semver.Version{Major: ocpVersion.Major, Minor: ocpVersion.Minor + 1}
			c := &kubeletVersionSkewController{
				operatorClient: v1helpers.NewFakeStaticPodOperatorClient(
					&operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}},
					status, nil, nil,
				),
				nodeLister:                  corev1listers.NewNodeLister(indexer),
				apiServerVersion:            apiServerVersion,
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
				t.Log(diff.Diff(tc.expectedMsgLines, condition.Message))
			}
			if t.Failed() {
				t.Logf("Condition message: %s", condition.Message)
			}
		})
	}
}
