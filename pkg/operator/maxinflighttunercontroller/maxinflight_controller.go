package maxinflighttunercontroller

import (
	"context"
	"fmt"
	"k8s.io/klog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/metrics"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	// If the total number of nodes exceeds this threshold we will double the values for
	// max-requests-inflight and max-mutating-requests-inflight.
	nodeThreshold = 500

	// If the total number of pods exceeds this threshold we will double the values for
	// max-requests-inflight and max-mutating-requests-inflight.
	podThreshold = 1000
)

// NewMaxInflightTunerController returns a new instance of the controller.
func NewMaxInflightTunerController(operatorClient operatorv1helpers.StaticPodOperatorClient, kubeClient kubernetes.Interface, metricsClient metricsclient.MetricsV1beta1Interface, eventRecorder events.Recorder) (factory.Controller, error) {
	// The default value for max-requests-inflight and max-mutating-requests-inflight are read from bindata asset. We
	// can't rely on ConfigMap to read the default values. The default values are read once and used throughout the
	// lifetime of the controller instance.
	defaults, err := ReadMaxInFlightValuesFromAsset()
	if err != nil {
		return nil, err
	}

	syncer := &MaxInflightTunerController{
		operatorClient: operatorClient,
		kubeClient:     kubeClient,
		metricsClient:  metrics.NewClient(metricsClient),
		defaults:       defaults,
	}

	// The controller will wake up after every ResyncInterval(1 hour) period and tune the max-requests-inflight and
	// max-mutating-requests-inflight values if necessary.
	// The controller is NOT backed up by any informer.
	controller := factory.New().ResyncEvery(1*time.Hour).WithSync(syncer.sync).ToController("MaxInFlightTunerController", eventRecorder)
	return controller, nil
}

// MaxInflightTunerController is a controller that runs periodically and tunes the values for max-requests-inflight
// and max-mutating-requests-inflight apiserver arguments if necessary.
type MaxInflightTunerController struct {
	operatorClient operatorv1helpers.StaticPodOperatorClient
	kubeClient     kubernetes.Interface
	metricsClient  *metrics.Client
	defaults       MaxInFlightValues
}

// sync runs every ResyncInterval period and performs the following operations:
//
// 1. it uses the metrics client to get Pod and Node metrics summary.
// 2. it determines whether we need to scale up or down and then it calculates the new values for
//    max-requests-inflight and max-mutating-requests-inflight accordingly.
// 3. it updates spec.UnsupportedConfigOverrides of kubeapiserver/cluster with the new values for max-requests-inflight and
//    max-mutating-requests-inflight.
//
// in current implementation either of the following triggers can cause the values for max-requests-inflight and
// max-mutating-requests-inflight to be doubled:
// - if the number of Pods exceeds the default threshold (defined by PodThreshold)
// - if the number of Nodes exceeds the default threshold (defined by NodeThreshold)
//
// For a scale up we double the default values. When we scale down we reset the values to defaults.
func (m *MaxInflightTunerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	summary, err := m.metricsClient.GetNodeAndPodSummary(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve metrics - %s", err.Error())
	}

	// The current values in use for max-requests-inflight and max-mutating-requests-inflight are read
	// from the 'config' ConfigMap at LatestAvailableRevision.
	configMap, err := m.readConfigMapAtLatestAvailableRevision(ctx)
	if err != nil {
		return fmt.Errorf("failed to read configmap at LatestAvailableRevision - %s", err.Error())
	}
	currentMaxInFlightValues, err := ReadMaxInFlightValuesFromConfigMap(configMap)
	if err != nil {
		return fmt.Errorf("failed to read max-inflight values from UnsupportedConfigOverrides - %s", err.Error())
	}

	desiredMaxInFlightValues := getDesiredMaxInFlightValues(m.defaults, summary)

	values := fmt.Sprintf("threshold:{node >= %d pod >= %d} current:{%s} desired:{%s} metrics:{%s} source:%s",
		nodeThreshold, podThreshold, currentMaxInFlightValues.String(), desiredMaxInFlightValues.String(), summary.String(), configMap.GetName())

	if !needsUpdate(currentMaxInFlightValues, desiredMaxInFlightValues) {
		klog.Infof("[max-inflight-tuner] no tuning required - %s", values)
		return nil
	}
	_, specUpdated, err := operatorv1helpers.UpdateSpec(m.operatorClient, func(spec *operatorv1.OperatorSpec) error {
		return WriteMaxInFlightValues(spec, desiredMaxInFlightValues)
	})
	if err != nil {
		return fmt.Errorf("failed to tune max-inflight arguments - %s - %s", values, err.Error())
	}
	if !specUpdated {
		return nil
	}

	klog.Infof("[max-inflight-tuner] successfully tuned - %s", values)
	syncCtx.Recorder().Eventf("MaxInFlightArgumentsUpdated", "[max-inflight-tuner] successfully tuned - %s", values)
	return nil
}

func (m *MaxInflightTunerController) readConfigMapAtLatestAvailableRevision(ctx context.Context) (*corev1.ConfigMap, error) {
	_, status, _, err := m.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return nil, err
	}

	// TODO: is this the right way to get the current configuration in effect?
	name := fmt.Sprintf("config-%d", status.LatestAvailableRevision)
	return m.kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, name, metav1.GetOptions{})
}

// based on the Pod and Node metrics summary it returns the desired values for max-requests-inflight and
// max-mutating-requests-inflight accordingly.
func getDesiredMaxInFlightValues(defaults MaxInFlightValues, summary *metrics.Summary) MaxInFlightValues {
	scale := false
	if summary.TotalNodes >= nodeThreshold || summary.TotalPods >= podThreshold {
		scale = true
	}

	readonly := *defaults.MaxReadOnlyInFlight
	mutating := *defaults.MaxMutatingInFlight
	if scale {
		readonly = readonly * 2
		mutating = mutating * 2
		// TODO: if the user has overridden values of max-requests-inflight and max-mutating-requests-inflight then
		//  these values will be reset by us. If the user-provided values are higher does that pose a problem?
	}

	// we are not keeping track of the original values specified by the user if any. so the best course of action
	// is to reset to default when we scale back.
	return MaxInFlightValues{
		MaxReadOnlyInFlight: &readonly,
		MaxMutatingInFlight: &mutating,
	}
}

func needsUpdate(current, desired MaxInFlightValues) bool {
	if desired.MaxReadOnlyInFlight == nil || desired.MaxMutatingInFlight == nil {
		return false
	}

	if current.MaxReadOnlyInFlight != nil && *current.MaxReadOnlyInFlight != *desired.MaxReadOnlyInFlight {
		return true
	}
	if current.MaxMutatingInFlight != nil && *current.MaxMutatingInFlight != *desired.MaxMutatingInFlight {
		return true
	}

	return false
}
