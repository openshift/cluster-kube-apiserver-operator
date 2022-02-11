package missingstaticpodcontroller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	corelisterv1 "k8s.io/client-go/listers/core/v1"
)

type MissingStaticPodController struct {
	operatorClient v1helpers.StaticPodOperatorClient
	podLister      corelisterv1.PodLister
}

// New checks the latest installer pods.  If the installer pod was successful, and if it has
// been longer than the terminationGracePeriodSeconds+10seconds since the installer pod
// completed successfully, and if the static pod is not at the correct revision, this
// controller will go degraded.
// It will also emit an event for detection in CI,
func New(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForTargetNamespace informers.SharedInformerFactory,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &MissingStaticPodController{
		operatorClient: operatorClient,
		podLister:      kubeInformersForTargetNamespace.Core().V1().Pods().Lister(),
	}
	return factory.New().
		WithInformers(
			operatorClient.Informer(),
			kubeInformersForTargetNamespace.Core().V1().Pods().Informer(),
		).
		WithSync(c.sync).
		WithSyncDegradedOnError(operatorClient).
		ToController("MissingStaticPodController", eventRecorder)
}

func (c MissingStaticPodController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	_, originalOperatorStatus, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return err
	}

	installerPods, err := c.podLister.List(labels.SelectorFromSet(labels.Set{"app": "installer"}))
	if err != nil {
		return err
	}

	// get the most recent installer pod for each node
	latestInstallerPodsByNode := getMostRecentInstallerPodByNode(installerPods)
	for node, latestInstallerPodOnNode := range latestInstallerPodsByNode {
		installerPodRevision, err := getInstallerPodRevision(latestInstallerPodOnNode)
		if err != nil {
			// we expect every installer pod to have a installerPodRevision in its name, unexpected error here
			return fmt.Errorf("failed to get installerPodRevision for installer pod %q - %w", latestInstallerPodOnNode.Name, err)
		}

		finishedAt, ok := installerPodFinishedAt(latestInstallerPodOnNode)
		if !ok {
			// either it's in the process of running, or it ran into an error
			continue
		}
		threshold := terminationGracePeriodSeconds(latestInstallerPodOnNode) + 10*time.Second
		staticPodRevisionOnThisNode := getStaticPodCurrentRevision(node, originalOperatorStatus)
		if time.Since(finishedAt) > threshold &&
			staticPodRevisionOnThisNode < installerPodRevision {
			// if we are here:
			//  a: the latest installer pod successfully completed at finishedAt
			//  b: it has been more than 'terminationGracePeriodSeconds' + 10s since the
			//     installer pod has completed at finishedAt
			//  c. the static pod is not at correct installerPodRevision yet
			// then all of the above conditions are true, so we should go degraded
			//
			// TODO: set a degraded condition
			syncCtx.Recorder().Eventf("MissingStaticPod", "static pod lifecycle failure - installer pod: %q with revision: %d completed at: %s, static pod revision: %d",
				latestInstallerPodOnNode.Name, installerPodRevision, finishedAt, staticPodRevisionOnThisNode)
		}
	}

	return nil
}

func getStaticPodCurrentRevision(node string, status *operatorv1.StaticPodOperatorStatus) int {
	var nodeStatus *operatorv1.NodeStatus
	for i := range status.NodeStatuses {
		if status.NodeStatuses[i].NodeName == node {
			nodeStatus = &status.NodeStatuses[i]
			break
		}
	}

	if nodeStatus == nil {
		return 0
	}
	return int(nodeStatus.CurrentRevision)
}

func terminationGracePeriodSeconds(pod *corev1.Pod) time.Duration {
	value := pod.Spec.TerminationGracePeriodSeconds
	if value == nil {
		return 0
	}

	return time.Duration(*value * int64(time.Second))
}

func getMostRecentInstallerPodByNode(pods []*corev1.Pod) map[string]*corev1.Pod {
	mostRecentInstallerPodByNode := map[string]*corev1.Pod{}
	byNodes := getInstallerPodsByNode(pods)
	for node, installerPodsOnThisNode := range byNodes {
		if len(installerPodsOnThisNode) == 0 {
			continue
		}
		sort.Sort(byRevision(installerPodsOnThisNode))
		mostRecentInstallerPodByNode[node] = installerPodsOnThisNode[len(installerPodsOnThisNode)-1]
	}

	return mostRecentInstallerPodByNode
}

func getInstallerPodsByNode(pods []*corev1.Pod) map[string][]*corev1.Pod {
	byNodes := map[string][]*corev1.Pod{}
	for i := range pods {
		pod := pods[i]
		if !strings.HasPrefix(pod.Name, "installer-") {
			continue
		}

		nodeName := pod.Spec.NodeName
		if len(nodeName) == 0 {
			continue
		}
		byNodes[nodeName] = append(byNodes[nodeName], pod)
	}

	return byNodes
}

type byRevision []*corev1.Pod

func (s byRevision) Len() int {
	return len(s)
}
func (s byRevision) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s byRevision) Less(i, j int) bool {
	jRevision, err := getInstallerPodRevision(s[j])
	if err != nil {
		return true
	}
	iRevision, err := getInstallerPodRevision(s[i])
	if err != nil {
		return false
	}
	return iRevision < jRevision
}

func getInstallerPodRevision(pod *corev1.Pod) (int, error) {
	tokens := strings.Split(pod.Name, "-")
	if len(tokens) < 2 {
		return -1, fmt.Errorf("missing revision: %v", pod.Name)
	}
	revision, err := strconv.ParseInt(tokens[1], 10, 32)
	if err != nil {
		return -1, fmt.Errorf("bad revision for %v: %w", pod.Name, err)
	}
	return int(revision), nil
}

// installerPodFinishedAt returns the 'finishedAt' time for an installer
// pod that has completed successfully
func installerPodFinishedAt(pod *corev1.Pod) (time.Time, bool) {
	statuses := pod.Status.ContainerStatuses
	if len(statuses) == 0 {
		return time.Time{}, false
	}

	// we are looking for container name "installer"
	var installerContainerStatus *corev1.ContainerStatus
	for i := range statuses {
		if statuses[i].Name == "installer" {
			installerContainerStatus = &statuses[i]
			break
		}
	}
	if installerContainerStatus == nil {
		return time.Time{}, false
	}

	terminated := installerContainerStatus.State.Terminated
	if terminated == nil || terminated.ExitCode != 0 {
		return time.Time{}, false
	}

	return terminated.FinishedAt.Time, true
}
