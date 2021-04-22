package e2e

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

func TestAPIRemovedInNextReleaseInUse(t *testing.T) {
	t.Run("RemovedRelease", func(t *testing.T) {
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
		removedRelease := strings.Split(regexp.MustCompile(`.*removed_release="(\d+\.\d+)".*`).ReplaceAllString(expr, "$1"), ".")
		require.Len(t, removedRelease, 2, "Unable to parse the removed release version from the alert expression.")
		major, err := strconv.Atoi(removedRelease[0])
		require.NoError(t, err)
		minor, err := strconv.Atoi(removedRelease[1])
		require.NoError(t, err)

		// rewrite this test if the major version ever changes
		require.Equal(t, currentMajor, major)
		require.Equal(t, currentMinor+1, minor)
	})
}
