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
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

func TestAPIRemovedInNextReleaseInUse(t *testing.T) {
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
	monitoringClient, err := monitoringclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rule, err := monitoringClient.MonitoringV1().PrometheusRules("openshift-kube-apiserver").Get(ctx, "api-usage", v1.GetOptions{})
	require.NoError(t, err)
	expr := func() string {
		for _, group := range rule.Spec.Groups {
			for _, rule := range group.Rules {
				if rule.Alert == "APIRemovedInNextReleaseInUse" {
					return strings.TrimSpace(rule.Expr.StrVal)
				}
			}
		}
		return ""
	}()
	require.NotEmpty(t, expr, "Unable to find the alert expression.")
	removedRelease := strings.Split(regexp.MustCompile(`(?s).*removed_release="(\d+\.\d+)".*`).ReplaceAllString(expr, "$1"), ".")
	require.Len(t, removedRelease, 2, "Unable to parse the removed release version from the alert expression.")
	major, err := strconv.Atoi(removedRelease[0])
	require.NoError(t, err)
	minor, err := strconv.Atoi(removedRelease[1])
	require.NoError(t, err)

	// rewrite this test if the major version ever changes
	require.Equal(t, currentMajor, major)
	require.Equal(t, currentMinor+1, minor)
}

func TestAPIRemovedInNextEUSReleaseInUse(t *testing.T) {
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
	monitoringClient, err := monitoringclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rule, err := monitoringClient.MonitoringV1().PrometheusRules("openshift-kube-apiserver").Get(ctx, "api-usage", v1.GetOptions{})
	require.NoError(t, err)
	expr := func() string {
		for _, group := range rule.Spec.Groups {
			for _, rule := range group.Rules {
				if rule.Alert == "APIRemovedInNextEUSReleaseInUse" {
					return strings.TrimSpace(rule.Expr.StrVal)
				}
			}
		}
		return ""
	}()
	require.NotEmpty(t, expr, "Unable to find the alert expression.")
	rx := regexp.MustCompile(`(?s).*removed_release="(\d+\.\d+)".*`)
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
	rx = regexp.MustCompile(`(?s).*removed_release=~"([^"]+)".*`)
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
