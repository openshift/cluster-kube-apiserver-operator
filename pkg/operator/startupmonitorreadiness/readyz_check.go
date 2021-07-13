package startupmonitorreadiness

import (
	"context"
	"fmt"
	"k8s.io/klog/v2"
	"time"

	"github.com/openshift/library-go/pkg/monitor/health"
	"github.com/openshift/library-go/pkg/operator/staticpod/startupmonitor"

	"k8s.io/client-go/rest"
)

type KubeAPIReadyzChecker struct {
	readyzMonitor *health.Prober
	restConfig    *rest.Config
}

var _ startupmonitor.HealthChecker = &KubeAPIReadyzChecker{}
var _ startupmonitor.WantsRestConfig = &KubeAPIReadyzChecker{}

func New() *KubeAPIReadyzChecker {
	return &KubeAPIReadyzChecker{}
}

func (ch *KubeAPIReadyzChecker) Start(ctx context.Context) error {
	klog.V(2).Info("Starting Kube API readyz monitor")
	defer klog.V(2).Info(" Kube API readyz monitor started")

	var err error
	targetProvider := health.StaticTargetProvider{"localhost:6443"}
	ch.readyzMonitor, err = health.New(targetProvider, ch.restConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize Kube API health monitior due to %v", err)
	}

	ch.readyzMonitor.WithHealthyProbesThreshold(3).
		WithUnHealthyProbesThreshold(5).
		WithProbeInterval(2 * time.Second).
		WithProbeResponseTimeout(1 * time.Second)

	go ch.readyzMonitor.Run(ctx)
	return nil
}

func (ch *KubeAPIReadyzChecker) SetRestConfig(restConfig *rest.Config) {
	ch.restConfig = restConfig
}

func (ch *KubeAPIReadyzChecker) IsTargetHealthy() (bool, string, error) {
	// TODO: update the health.Prober to accept path, query params (verbosity) and make it return errors for unhealthy targets
	healthyTargets, _ := ch.readyzMonitor.Targets()
	return len(healthyTargets) == 1, "", nil
}
