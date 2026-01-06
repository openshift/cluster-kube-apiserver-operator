package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	g "github.com/onsi/ginkgo/v2"
)

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator", func() {
	g.It("[Operator][Serial] TestAPIRemovedInNextReleaseInUse", func() {
		testAPIRemovedInNextReleaseInUse(g.GinkgoTB())
	})
	g.It("[Operator][Serial] TestAPIRemovedInNextEUSReleaseInUse", func() {
		testAPIRemovedInNextEUSReleaseInUse(g.GinkgoTB())
	})
})

func testAPIRemovedInNextReleaseInUse(t testing.TB) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	// get current major.minor version
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeConfig)
	require.NoError(t, err)
	version, err := discoveryClient.ServerVersion()
	require.NoError(t, err)
	currentMajor, err := strconv.Atoi(version.Major)
	require.NoError(t, err)
	currentMinor, err := strconv.Atoi(regexp.MustCompile(`^\d*`).FindString(version.Minor))

	// get deprecated major.minor version from alert expression
	// NOTE: the alert major and minor version is hardcoded
	// this test will fail in each version bump until the alert is updated
	// xref: https://github.com/openshift/cluster-kube-apiserver-operator/blob/master/bindata/assets/alerts/api-usage.yaml
	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, err)
	expr := retrieveAlertExpression(t, ctx, dynamicClient, "APIRemovedInNextReleaseInUse")
	require.NotEmpty(t, expr, "Unable to find the alert expression.")
	removedRelease := strings.Split(regexp.MustCompile(`.*removed_release="(\d+\.\d+)".*`).ReplaceAllString(expr, "$1"), ".")
	require.Len(t, removedRelease, 2, "Unable to parse the removed release version from the alert expression.")
	major, err := strconv.Atoi(removedRelease[0])
	require.NoError(t, err)
	minor, err := strconv.Atoi(removedRelease[1])
	require.NoError(t, err)

	// rewrite this test if the major version ever changes
	require.Equal(t, currentMajor, major)
	require.Equal(t, currentMinor+1, minor)
}

func testAPIRemovedInNextEUSReleaseInUse(t testing.TB) {
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)

	// get current major.minor version
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeConfig)
	require.NoError(t, err)
	version, err := discoveryClient.ServerVersion()
	require.NoError(t, err)
	currentMajor, err := strconv.Atoi(version.Major)
	require.NoError(t, err)
	currentMinor, err := strconv.Atoi(regexp.MustCompile(`^\d*`).FindString(version.Minor))

	// get deprecated major.minor version from alert expression
	// NOTE: the alert major and minor version is hardcoded
	// this test will fail in each version bump until the alert is updated
	// xref: https://github.com/openshift/cluster-kube-apiserver-operator/blob/master/bindata/assets/alerts/api-usage.yaml
	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, err)
	expr := retrieveAlertExpression(t, ctx, dynamicClient, "APIRemovedInNextEUSReleaseInUse")
	require.NotEmpty(t, expr, "Unable to find the alert expression.")
	rx := regexp.MustCompile(`.*removed_release="(\d+\.\d+)".*`)
	if rx.FindStringIndex(expr) != nil {
		// kubernetes minor version must be even, otherwise this is an even numbered
		// OpenShift EUS release and the alert can't match 2 versions using the label
		// matching operator `=` and must instead use `~=` instead.
		require.Equal(t, 0, currentMinor%2, "Alert expression should match more than one release.")

		removedRelease := strings.Split(rx.ReplaceAllString(expr, "$1"), ".")
		require.Len(t, removedRelease, 2, "Unable to parse the removed release version from the alert expression.")
		major, err := strconv.Atoi(removedRelease[0])
		require.NoError(t, err)
		minor, err := strconv.Atoi(removedRelease[1])
		require.NoError(t, err)

		// rewrite this test if the major version ever changes
		require.Equal(t, currentMajor, major)
		require.Equal(t, currentMinor+1, minor)
		return
	}
	rx = regexp.MustCompile(`.*removed_release=~"([^"]+)".*`)
	require.Regexp(t, rx, expr, "Unable to parse the removed release version from the alert expression.")
	removedRelease := strings.ReplaceAll(rx.ReplaceAllString(expr, "$1"), `\\`, `\`)

	// rewrite this test if the major version ever changes

	// if Kubernetes minor version is even, this is an odd numbered OpenShift non-EUS
	// release and the next EUS release is one minor versions away; otherwise, we still
	// alert on the next version to forewarn EUS customers.
	assert.Regexp(t, removedRelease, fmt.Sprintf("%d.%d", currentMajor, currentMinor+1))

	// if Kubernetes minor version is odd, this is an even numbered OpenShift
	// EUS release and the next EUS release is two minor versions away.
	if currentMinor%2 == 1 {
		assert.Regexp(t, removedRelease, fmt.Sprintf("%d.%d", currentMajor, currentMinor+2))
	}

}

func retrieveAlertExpression(t testing.TB, ctx context.Context, client *dynamic.DynamicClient, alertName string) string {
	prometheusRulesGVR := schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"}
	prometheusRule, err := client.Resource(prometheusRulesGVR).Namespace("openshift-kube-apiserver").Get(ctx, "api-usage", metav1.GetOptions{})
	require.NoError(t, err)
	groups, _, err := unstructured.NestedSlice(prometheusRule.UnstructuredContent(), "spec", "groups")
	require.NoError(t, err)
	for _, group := range groups {
		rules, _, err := unstructured.NestedSlice(group.(map[string]interface{}), "rules")
		require.NoError(t, err)
		for _, rule := range rules {
			alert, _, err := unstructured.NestedString(rule.(map[string]interface{}), "alert")
			require.NoError(t, err)
			if alert == alertName {
				expr, _, err := unstructured.NestedString(rule.(map[string]interface{}), "expr")
				require.NoError(t, err)
				return strings.TrimSpace(expr)
			}
		}
	}
	return ""
}
