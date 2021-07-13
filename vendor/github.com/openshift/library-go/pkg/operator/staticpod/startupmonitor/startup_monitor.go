package startupmonitor

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// HealthChecker a contract between the startup monitor and operators
// allows for initialization and an operand's health condition assessment
type HealthChecker interface {
	// Start is used to initialize and start the underlying health monitor
	Start(context.Context) error

	// IsTargetHealthy defines a function that abstracts away assessing operand's health condition.
	// the provided functions should be async and cheap in a sense that it shouldn't assess the target
	// only read the current state.
	// mainly because we acquire a lock on each sync.
	IsTargetHealthy() (healthy bool, reason string, err error)
}

// WantsRestConfig an optional interface used for setting rest config for Kube API
type WantsRestConfig interface {
	SetRestConfig(config *rest.Config)
}

// monitor is a controller that watches an operand's health condition
// and falls back to the previous version in case the current version is considered unhealthy.
//
// This controller understands a tree structure created by an OCP installation. That is:
//  The root manifest are looked up in the manifestPath
//  The revisioned manifest are looked up in the staticPodResourcesPath
//  The target (operand) name is derived from the targetName.
type monitor struct {
	// probeInterval specifies a time interval at which health of the target will be assessed
	// be mindful of not setting it too low, on each iteration, an i/o is involved
	probeInterval time.Duration

	// timeout specifies a timeout after which the monitor starts the fall back procedure
	timeout time.Duration

	// revision at which the monitor was started
	revision int

	// targetName hold the name of the operand
	// used to construct the final file name when reading the current and previous manifests
	targetName string

	// manifestsPath points to the directory that holds the root manifests
	manifestsPath string

	// staticPodResourcesPath points to the directory that holds all files supporting the static pod manifests
	staticPodResourcesPath string

	// healthChecker is used to get the current operand's health condition
	healthChecker HealthChecker

	// records the time the monitor has started assessing operand's health condition
	monitorTimeStamp time.Time

	// io collects file system level operations that need to be mocked out during tests
	io ioInterface
}

func newMonitor(restConfig *rest.Config, healthChecker HealthChecker) *monitor {
	if wants, ok := healthChecker.(WantsRestConfig); ok {
		wants.SetRestConfig(restConfig)
	}

	return &monitor{healthChecker: healthChecker, io: realFS{}}
}

func (m *monitor) Run(ctx context.Context) error {
	klog.Infof("Starting the startup monitor with Interval = %v, Timeout = %v", m.probeInterval, m.timeout)
	defer klog.Info("Shutting down the startup monitor")

	if err := m.healthChecker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start the health checker for %s, due to %v", m.targetName, err)
	}

	wait.Until(m.syncErrorWrapper, m.probeInterval, ctx.Done())
	return nil
}

func (m *monitor) syncErrorWrapper() {
	if err := m.sync(); err != nil {
		klog.Error(err)
	}
}

func (m *monitor) sync() error {
	//
	// TODO: acquire an exclusive lock to coordinate work with the installer pod
	//
	// a lock is required to protect the following case:
	//
	// an installer is in progress and wants to install a new revision
	// the current revision is not healthy and we are about to fall back to the previous version (fallbackToPreviousRevision method)
	// the installer writes the new file and we immediately overwrite it
	//
	// additional benefit is that we read consistent operand's manifest

	// to avoid issues on startup and downgrade (before the startup monitor was introduced check the current target's revision.
	// refrain from any further processing in case we have a mismatch.
	currentTargetRevision, err := m.loadRootTargetPodAndExtractRevision()
	if err != nil {
		return err
	}
	if m.revision != currentTargetRevision {
		klog.Infof("Stopping further processing because the monitor is watching revision %d and the current target's revision is %d", m.revision, currentTargetRevision)
		return nil
	}

	if m.monitorTimeStamp.IsZero() {
		m.monitorTimeStamp = time.Now()
	}

	// first check if the target is healthy
	// note that we will always reconcile on transient errors
	// before starting the fall back procedure
	healthy, reason, err := m.healthChecker.IsTargetHealthy()
	if healthy {
		klog.Info("Observed a healthy target, creating last known good revision")
		if err := m.createLastKnowGoodRevisionAndDestroy(); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		klog.Infof("failed to assess health condition due to err = %v, reason = %v", err, reason)
	}

	// check if we reached the timeout
	if time.Now().After(m.monitorTimeStamp.Add(m.timeout)) {
		klog.Infof("Timed out while waiting for the target to become healthy, starting a fallback procedure. Timeout is %v, MonitorTS is %v, CurrentTS is %v", m.timeout, m.monitorTimeStamp, time.Now())
		// TODO: report reason and err
		_ = reason
		_ = err
		if err := m.fallbackToPreviousRevision(); err != nil {
			return err
		}
		return nil
	}

	return nil
}

func (m *monitor) createLastKnowGoodRevisionAndDestroy() error {
	// step 0: rm the previous last good known revision if exists
	// step 1: create last known good revision
	if err := m.createLastKnowGoodRevisionFor(m.revision, true); err != nil {
		return err
	}

	// step 2: commit suicide
	return m.io.Remove(path.Join(m.manifestsPath, fmt.Sprintf("%s-startup-monitor.yaml", m.targetName)))
}

// TODO: pruner|installer: protect the linked revision
func (m *monitor) fallbackToPreviousRevision() error {
	// step 0: if the last known good revision doesn't exist
	//         find a previous revision to work with
	//         return in case no revision has been found
	//           TODO: or commit suicide as this seems to be fatal
	lastKnownExists, err := m.fileExists(m.lastKnownGoodManifestDstPath())
	if err != nil {
		return err
	}
	if !lastKnownExists {
		prevRev, found, err := m.findPreviousRevision()
		if err != nil {
			return err
		}
		if !found {
			klog.Info("Unable to roll back because no previous revision hasn't been found for %s", m.targetName)
			// TODO: commit suicide ? this seems to be fatal
			return nil
		}

		targetManifestForPrevRevExists, err := m.fileExists(m.targetManifestPathFor(prevRev))
		if err != nil {
			return err // retry, a transient err
		}
		if !targetManifestForPrevRevExists {
			klog.Info("Unable to roll back because a manifest %q hasn't been found for the previous revision %d", m.targetManifestPathFor(prevRev), prevRev)
			// TODO: commit suicide ? this seems to be fatal
			return nil
		}

		// step 1: create the last known good revision file
		if err := m.createLastKnowGoodRevisionFor(prevRev, false); err != nil {
			return err
		}
	}

	// step 2: if the last known good revision exits and we got here
	//         that could mean that:
	//          - the current revision is broken
	//          - we just created the last known good revision file
	//          - the previous iteration of the sync loop returned an error
	//
	//         in that case just:
	//          - annotate the manifest
	//          - copy the last known good revision manifest
	lastKnownGoodPod, err := m.readTargetPod(m.lastKnownGoodManifestDstPath())
	if err != nil {
		return err
	}
	if lastKnownGoodPod.Annotations == nil {
		lastKnownGoodPod.Annotations = map[string]string{}
	}
	lastKnownGoodPod.Annotations["startup-monitor.static-pods.openshift.io/fallback-for-revision"] = fmt.Sprintf("%d", m.revision)

	// the kubelet has a bug that prevents graceful termination from working on static pods with the same name, filename
	// and uuid.  By setting the pod UID we can work around the kubelet bug and get our graceful termination honored.
	// Per the node team, this is hard to fix in the kubelet, though it will affect all static pods.
	lastKnownGoodPod.UID = uuid.NewUUID()

	// remove the existing file to ensure kubelet gets "create" event from inotify watchers
	rootTargetManifestPath := path.Join(m.manifestsPath, fmt.Sprintf("%s-pod.yaml", m.targetName))
	if err := m.io.Remove(rootTargetManifestPath); err == nil {
		klog.Infof("Removed existing static pod manifest %q", path.Join(rootTargetManifestPath))
	} else if !os.IsNotExist(err) {
		return err
	}

	lastKnownGoodPodBytes := []byte(resourceread.WritePodV1OrDie(lastKnownGoodPod))
	klog.Infof("Writing a static pod manifest %q \n%s", path.Join(rootTargetManifestPath), lastKnownGoodPodBytes)
	if err := m.io.WriteFile(path.Join(rootTargetManifestPath), lastKnownGoodPodBytes, 0644); err != nil {
		return err
	}

	// TODO: commit suicide ?
	return nil
}

func (m *monitor) createLastKnowGoodRevisionFor(revision int, strict bool) error {
	var revisionedTargetManifestPath = m.targetManifestPathFor(revision)

	// step 0: in strict mode remove the previous last good known revision if exists
	if strict {
		if exists, err := m.fileExists(m.lastKnownGoodManifestDstPath()); err != nil {
			return err
		} else if exists {
			if err := m.io.Remove(m.lastKnownGoodManifestDstPath()); err != nil {
				return err
			}
			klog.Info("Removed existing last known good revision manifest %s", m.lastKnownGoodManifestDstPath())
		}
	}

	// step 1: create last known good revision
	if err := m.io.Symlink(revisionedTargetManifestPath, m.lastKnownGoodManifestDstPath()); err != nil {
		return fmt.Errorf("failed to create a symbolic link %q for %q due to %v", m.lastKnownGoodManifestDstPath(), revisionedTargetManifestPath, err)
	}
	klog.Infof("Created a symlink %s for %s", m.lastKnownGoodManifestDstPath(), revisionedTargetManifestPath)
	return nil
}

// note that there is a fight between the installer pod (writer) and the startup monitor (reader) when dealing with the target manifest file.
// since the monitor is resynced every probeInterval it seems we can deal with an error or stale content
//
// note if this code will return buffered data due to perf reason revisit fallbackToPreviousRevision
// as it currently assumes strong consistency
func (m *monitor) loadRootTargetPodAndExtractRevision() (int, error) {
	currentTargetPod, err := m.readTargetPod(path.Join(m.manifestsPath, fmt.Sprintf("%s-pod.yaml", m.targetName)))
	if err != nil {
		return 0, err
	}

	revisionString, found := currentTargetPod.Labels["revision"]
	if !found {
		return 0, fmt.Errorf("pod %s doesn't have revision label", currentTargetPod.Name)
	}
	if len(revisionString) == 0 {
		return 0, fmt.Errorf("empty revision label on %s pod", currentTargetPod.Name)
	}
	revision, err := strconv.Atoi(revisionString)
	if err != nil || revision < 0 {
		return 0, fmt.Errorf("invalid revision label on pod %s: %q", currentTargetPod.Name, revisionString)
	}

	return revision, nil
}

func (m *monitor) readTargetPod(filepath string) (*corev1.Pod, error) {
	rawManifest, err := m.io.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	currentTargetPod, err := resourceread.ReadPodV1(rawManifest)
	if err != nil {
		return nil, err
	}
	return currentTargetPod, nil
}

func (m *monitor) findPreviousRevision() (int, bool, error) {
	files, err := m.io.ReadDir(m.staticPodResourcesPath)
	if err != nil {
		return 0, false, err
	}

	var allRevisions []int
	for _, file := range files {
		// skip if the file is not a directory
		if !file.IsDir() {
			continue
		}

		// and doesn't match our prefix
		if !strings.HasPrefix(file.Name(), m.targetName+"-pod") {
			continue
		}

		klog.Infof("Considering %s for revision extraction", file.Name())
		// now split the file name to get just the revision
		fileSplit := strings.Split(file.Name(), m.targetName+"-pod-")
		if len(fileSplit) != 2 {
			// TODO: maybe we should continiue instead ?
			return 0, false, fmt.Errorf("unable to extract revision from %s due to incorrect format", file.Name())
		}
		revision, err := strconv.Atoi(fileSplit[1])
		if err != nil {
			return 0, false, err
		}
		allRevisions = append(allRevisions, revision)
	}

	if len(allRevisions) < 2 {
		return 0, false, nil
	}
	sort.IntSlice(allRevisions).Sort()
	return allRevisions[len(allRevisions)-2], true, nil
}

func (m *monitor) fileExists(filepath string) (bool, error) {
	fileInfo, err := m.io.Stat(filepath)
	if err == nil {
		if fileInfo.IsDir() {
			return false, fmt.Errorf("the provided path %v is incorrect and points to a directory", filepath)
		}
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	return false, nil
}

func (m *monitor) lastKnownGoodManifestDstPath() string {
	return path.Join(m.staticPodResourcesPath, fmt.Sprintf("%s-last-known-good", m.targetName))
}

func (m *monitor) targetManifestPathFor(revision int) string {
	return path.Join(m.staticPodResourcesPath, fmt.Sprintf("%s-pod-%d", m.targetName, revision), fmt.Sprintf("%s-pod.yaml", m.targetName))
}
