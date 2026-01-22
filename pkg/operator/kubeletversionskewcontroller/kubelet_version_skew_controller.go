package kubeletversionskewcontroller

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	corev1listers "k8s.io/client-go/listers/core/v1"
	cache "k8s.io/client-go/tools/cache"
)

const (
	KubeletMinorVersionUpgradeableConditionType = "KubeletMinorVersionUpgradeable"

	KubeletVersionUnknownReason                     = "KubeletVersionUnknown"
	KubeletMinorVersionSyncedReason                 = "KubeletMinorVersionsSynced"
	KubeletMinorVersionSupportedNextUpgradeReason   = "KubeletMinorVersionSupportedNextUpgrade"
	KubeletMinorVersionUnsupportedNextUpgradeReason = "KubeletMinorVersionUnsupportedNextUpgrade"
	KubeletMinorVersionUnsupportedReason            = "KubeletMinorVersionUnsupported"
	KubeletMinorVersionAheadReason                  = "KubeletMinorVersionAhead"
)

// KubeletVersionSkewController sets Upgradeable=False if the kubelet
// version on a node prevents upgrading to a supported OpenShift version.
//
// For odd OpenShift minor versions, kubelet versions 0 or 1 minor
// versions behind the API server version are supported.
//
// For even OpenShift minor versions, kubelet versions 0, 1, or 2
// minor versions behind the API server version are supported.
type KubeletVersionSkewController interface {
	factory.Controller
}

func NewKubeletVersionSkewController(
	operatorClient v1helpers.OperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	nodeLister corev1listers.NodeLister,
	nodeInformer cache.SharedIndexInformer,
	recorder events.Recorder,
) *kubeletVersionSkewController {
	openShiftVersion := semver.MustParse(status.VersionForOperatorFromEnv())
	nextOpenShiftVersion := semver.Version{Major: openShiftVersion.Major, Minor: openShiftVersion.Minor + 1}
	c := &kubeletVersionSkewController{
		operatorClient:              operatorClient,
		nodeLister:                  nodeLister,
		apiServerVersion:            semver.MustParse(status.VersionForOperandFromEnv()),
		minSupportedSkew:            minSupportedKubeletSkewForOpenShiftVersion(openShiftVersion),
		minSupportedSkewNextVersion: minSupportedKubeletSkewForOpenShiftVersion(nextOpenShiftVersion),
		nextOpenShiftVersion:        nextOpenShiftVersion,
	}
	c.Controller = factory.New().
		WithSync(c.sync).
		WithInformers(nodeInformer).
		ToController("KubeletVersionSkewController", recorder.WithComponentSuffix("kubelet-version-skew-controller"))
	return c
}

func minSupportedKubeletSkewForOpenShiftVersion(v semver.Version) int {
	switch v.Minor % 2 {
	case 0: // even OpenShift versions
		return -2
	case 1: // odd OpenShift versions
		return -1
	default:
		panic("should not happen")
	}
}

type kubeletVersionSkewController struct {
	factory.Controller
	operatorClient              v1helpers.OperatorClient
	nodeLister                  corev1listers.NodeLister
	apiServerVersion            semver.Version
	minSupportedSkew            int
	minSupportedSkewNextVersion int
	nextOpenShiftVersion        semver.Version
}

func (c *kubeletVersionSkewController) sync(ctx context.Context, _ factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if !management.IsOperatorManaged(operatorSpec.ManagementState) {
		return nil
	}

	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return err
	}
	sort.Sort(byName(nodes))

	var errors nodeKubeletInfos
	var skewedUnsupported nodeKubeletInfos
	var skewedLimit nodeKubeletInfos
	var skewedButOK nodeKubeletInfos
	var synced nodeKubeletInfos
	var unsupported nodeKubeletInfos

	// for each node, check kubelet version
	for _, node := range nodes {
		kubeletVersion, err := nodeKubeletVersion(node)
		if err != nil {
			runtime.HandleError(fmt.Errorf("unable to determine kubelet version on node %s: %w", node.Name, err))
			errors = append(errors, nodeKubeletInfo{node: node.Name, err: err})
			continue
		}
		skew := int(kubeletVersion.Minor - c.apiServerVersion.Minor)
		// Assume that an OpenShift minor version upgrade also bumps to the next kube minor version. Revisit
		// this in the future if an OpenShift minor version upgrade ever skips or repeats a kube minor version.
		skewNextVersion := skew - 1
		switch {
		case skew == 0:
			// synced
			synced = append(synced, nodeKubeletInfo{node: node.Name, version: &kubeletVersion})
		case skew < c.minSupportedSkew:
			// already in an unsupported state
			skewedUnsupported = append(skewedUnsupported, nodeKubeletInfo{node: node.Name, version: &kubeletVersion})
		case skewNextVersion < c.minSupportedSkewNextVersion:
			// upgrading to next minor version of API server would result in an unsupported config
			skewedLimit = append(skewedLimit, nodeKubeletInfo{node: node.Name, version: &kubeletVersion})
		case skew < 0:
			// behind, but upgrading to next minor version of API server is supported
			skewedButOK = append(skewedButOK, nodeKubeletInfo{node: node.Name, version: &kubeletVersion})
		default:
			// kubelet version newer than api server version. possibly in the middle of a rollback.
			unsupported = append(unsupported, nodeKubeletInfo{node: node.Name, version: &kubeletVersion})
		}
	}

	condition := operatorv1.OperatorCondition{Type: KubeletMinorVersionUpgradeableConditionType}
	// use the most "severe" reason to set the condition status
	switch {
	case len(skewedUnsupported) > 0:
		condition.Reason = KubeletMinorVersionUnsupportedReason
		condition.Status = operatorv1.ConditionFalse
		switch len(skewedUnsupported) {
		case 1:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor version (%v) on node %s is too far behind the target API server version (%v).", skewedUnsupported.version(), skewedUnsupported.nodes(), c.apiServerVersion)
		case 2, 3:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor versions on nodes %s are too far behind the target API server version (%v).", skewedUnsupported.nodes(), c.apiServerVersion)
		default:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor versions on %d nodes are too far behind the target API server version (%v).", len(skewedUnsupported), c.apiServerVersion)
		}
	case len(unsupported) > 0:
		condition.Reason = KubeletMinorVersionAheadReason
		condition.Status = operatorv1.ConditionUnknown
		switch len(unsupported) {
		case 1:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor version (%v) on node %s is ahead of the target API server version (%v).", unsupported.version(), unsupported.nodes(), c.apiServerVersion)
		case 2, 3:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor versions on nodes %s are ahead of the target API server version (%v).", unsupported.nodes(), c.apiServerVersion)
		default:
			condition.Message = fmt.Sprintf("Unsupported kubelet minor versions on %d nodes are ahead of the target API server version (%v).", len(unsupported), c.apiServerVersion)
		}
	case len(errors) > 0:
		condition.Reason = KubeletVersionUnknownReason
		condition.Status = operatorv1.ConditionUnknown
		switch len(errors) {
		case 1:
			condition.Message = fmt.Sprintf("Unable to determine the kubelet version on node %s: %v", errors.nodes(), errors.error())
		case 2, 3:
			condition.Message = fmt.Sprintf("Unable to determine the kubelet version on nodes %s.", errors.nodes())
		default:
			condition.Message = fmt.Sprintf("Unable to determine the kubelet version on %d nodes.", len(errors))
		}
	case len(skewedLimit) > 0:
		condition.Reason = KubeletMinorVersionUnsupportedNextUpgradeReason
		condition.Status = operatorv1.ConditionFalse
		switch len(skewedLimit) {
		case 1:
			condition.Message = fmt.Sprintf("Kubelet minor version (%v) on node %s will not be supported in the next OpenShift minor version upgrade to %d.%d.", skewedLimit.version(), skewedLimit.nodes(), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		case 2, 3:
			condition.Message = fmt.Sprintf("Kubelet minor versions on nodes %s will not be supported in the next OpenShift minor version upgrade to %d.%d.", skewedLimit.nodes(), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		default:
			condition.Message = fmt.Sprintf("Kubelet minor versions on %d nodes will not be supported in the next OpenShift minor version upgrade to %d.%d.", len(skewedLimit), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		}
	case len(skewedButOK) > 0:
		condition.Reason = KubeletMinorVersionSupportedNextUpgradeReason
		condition.Status = operatorv1.ConditionTrue
		switch len(skewedButOK) {
		case 1:
			condition.Message = fmt.Sprintf("Kubelet minor version (%v) on node %s is behind the expected API server version; nevertheless, it will continue to be supported in the next OpenShift minor version upgrade to %d.%d.", skewedButOK.version(), skewedButOK.nodes(), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		case 2, 3:
			condition.Message = fmt.Sprintf("Kubelet minor versions on nodes %s are behind the expected API server version; nevertheless, they will continue to be supported in the next OpenShift minor version upgrade to %d.%d.", skewedButOK.nodes(), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		default:
			condition.Message = fmt.Sprintf("Kubelet minor versions on %d nodes are behind the expected API server version; nevertheless, they will continue to be supported in the next OpenShift minor version upgrade to %d.%d.", len(skewedButOK), c.nextOpenShiftVersion.Major, c.nextOpenShiftVersion.Minor)
		}
	default:
		condition.Reason = KubeletMinorVersionSyncedReason
		condition.Status = operatorv1.ConditionTrue
		condition.Message = "Kubelet and API server minor versions are synced."
	}

	_, _, err = v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition))
	return err
}

type nodeKubeletInfo struct {
	node    string
	version *semver.Version
	err     error
}

type nodeKubeletInfos []nodeKubeletInfo

func (n nodeKubeletInfos) nodes() string {
	var s []string
	for _, i := range n {
		s = append(s, i.node)
	}
	switch len(s) {
	case 0, 1:
	case 2:
		return strings.Join(s, " and ")
	default:
		s[len(s)-1] = "and " + s[len(s)-1]
	}
	return strings.Join(s, ", ")
}

func (n nodeKubeletInfos) error() error {
	if len(n) > 0 {
		return n[0].err
	}
	return nil
}

func (n nodeKubeletInfos) version() *semver.Version {
	if len(n) > 0 {
		return n[0].version
	}
	return nil
}

func nodeKubeletVersion(node *corev1.Node) (semver.Version, error) {
	return semver.Parse(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v"))
}

var byNodeRegexp = regexp.MustCompile(`node [^ ]*`)

type byName []*corev1.Node

func (n byName) Len() int           { return len(n) }
func (n byName) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n byName) Less(i, j int) bool { return strings.Compare(n[i].Name, n[j].Name) < 0 }
