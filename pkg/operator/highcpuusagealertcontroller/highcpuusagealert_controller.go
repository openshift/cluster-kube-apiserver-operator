package highcpuusagealertcontroller

import (
	"bytes"
	"context"
	"strconv"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/utils/cpuset"

	"k8s.io/klog/v2"
)

// default and taken from the docs
const defaultCoresNum = 8

var performanceGroup = schema.GroupVersionResource{Group: "performance.openshift.io", Version: "v2", Resource: "performanceprofiles"}

type highCPUUsageAlertController struct {
	client               dynamic.Interface
	infraLister          configlistersv1.InfrastructureLister
	clusterVersionLister configlistersv1.ClusterVersionLister
}

func NewHighCPUUsageAlertController(
	configInformer configv1informers.Interface,
	dynamicInformersForTargetNamespace dynamicinformer.DynamicSharedInformerFactory,
	client dynamic.Interface,
	recorder events.Recorder,
) factory.Controller {
	c := &highCPUUsageAlertController{
		client:               client,
		infraLister:          configInformer.Infrastructures().Lister(),
		clusterVersionLister: configInformer.ClusterVersions().Lister(),
	}

	prometheusAlertInformerForTargetNamespace := dynamicInformersForTargetNamespace.ForResource(schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "prometheusrules",
	})

	return factory.New().
		WithInformers(configInformer.Infrastructures().Informer(), configInformer.ClusterVersions().Informer(), prometheusAlertInformerForTargetNamespace.Informer()).
		WithSync(c.sync).ResyncEvery(10*time.Minute).
		ToController("highCPUUsageAlertController", recorder.WithComponentSuffix("high-cpu-usage-alert-controller"))
}

func (c *highCPUUsageAlertController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Infof("highCPUUsageAlertController: calling sync for %s", syncCtx.QueueKey())
	defer v1helpers.Timer("CertRotationTimeUpgradeableController")()
	infra, err := c.infraLister.Get("cluster")
	if err != nil {
		return err
	}

	var alertRaw []byte

	if infra.Status.InfrastructureTopology != configv1.SingleReplicaTopologyMode {
		// we moved creation of the alert here because the static resource controller was constantly
		// deleting the alert and was fighting with this controller
		alertRaw, err = bindata.Asset("assets/alerts/cpu-utilization.yaml")
		if err != nil {
			return err
		}
	} else {
		clusterVersion, err := c.clusterVersionLister.Get("version")
		if err != nil {
			return err
		}

		alertRaw, err = snoAlert(ctx, c.client, clusterVersion.Status.Capabilities.EnabledCapabilities, infra.Status.CPUPartitioning)
		if err != nil {
			return err
		}
	}

	alertObj, err := resourceread.ReadGenericWithUnstructured(alertRaw)
	if err != nil {
		return err
	}

	_, _, err = resourceapply.ApplyPrometheusRule(ctx, c.client, syncCtx.Recorder(), alertObj.(*unstructured.Unstructured))
	return err
}

func snoAlert(ctx context.Context, client dynamic.Interface, enabledCapabilities []configv1.ClusterVersionCapability, cpuMode configv1.CPUPartitioningMode) ([]byte, error) {
	cores := defaultCoresNum

	// if NodeTuning capability disabled, there are no PerformanceProfile, so we proceed
	// with default value.
	if sets.New(enabledCapabilities...).Has(configv1.ClusterVersionCapabilityNodeTuning) && cpuMode == configv1.CPUPartitioningAllNodes {
		foundCores, found, err := performanceProfileControlPlaneCores(ctx, client)
		if err != nil {
			return nil, err
		}
		// set cores from PerformanceProfile if expectedToFindCores
		// if not, proceed with default values
		if found {
			cores = foundCores
		}
	}

	fileData, err := bindata.Asset("assets/alerts/cpu-utilization-sno.yaml")
	if err != nil {
		return nil, err
	}
	fileData = bytes.ReplaceAll(fileData, []byte(`${CPU-COUNT}`), []byte(strconv.Itoa(cores)))

	return fileData, nil
}

// performanceProfileControlPlaneCores returns cores allocated for control plane pods via
// PerformanceProfile object. Bool value indicates if PerformanceProfile is expectedToFindCores for master node
func performanceProfileControlPlaneCores(ctx context.Context, client dynamic.Interface) (int, bool, error) {
	// fetch resource directly instead of using an informer because
	// NodeTuning capability can be disabled at start and enabled later
	obj, err := client.Resource(performanceGroup).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, false, err
	}

	for _, pf := range obj.Items {
		nodeSelector, found, err := unstructured.NestedStringMap(pf.Object, "spec", "nodeSelector")
		if err != nil {
			return 0, false, err
		}
		if !found {
			continue
		}
		if _, ok := nodeSelector["node-role.kubernetes.io/master"]; !ok {
			continue
		}

		reservedCPU, found, err := unstructured.NestedString(pf.Object, "spec", "cpu", "reserved")
		if err != nil {
			return 0, false, err
		}
		if !found {
			continue
		}

		cores, err := coresInCPUSet(reservedCPU)
		if err != nil {
			return 0, false, err
		}
		return cores, true, nil
	}

	return 0, false, nil
}

func coresInCPUSet(set string) (int, error) {
	cpuMap, err := cpuset.Parse(set)
	return cpuMap.Size(), err
}
