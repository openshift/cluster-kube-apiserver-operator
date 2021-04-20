package gracefulmonitor

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/davecgh/go-spew/spew"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

const (
	defaultSecurePort         = 6443
	defaultInsecurePort       = 6080
	defaultCheckEndpointsPort = 17697
)

type GracefulMonitorOptions struct {
	PodManifestDir string
}

func NewGracefulMonitorCommand() *cobra.Command {
	o := GracefulMonitorOptions{}

	cmd := &cobra.Command{
		Use:   "graceful-monitor",
		Short: "Monitors static pod state and ensures a graceful transition between old and new pods.",
		Run: func(cmd *cobra.Command, args []string) {
			klog.V(1).Info(cmd.Flags())
			klog.V(1).Info(spew.Sdump(o))

			if err := o.Validate(); err != nil {
				klog.Exit(err)
			}

			if err := o.Run(); err != nil {
				klog.Exit(err)
			}
		},
	}

	o.AddFlags(cmd.Flags())

	return cmd
}

func (o *GracefulMonitorOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.PodManifestDir, "pod-manifest-dir", "/etc/kubernetes/manifests", "directory for the static pod manifests")
}

func (o *GracefulMonitorOptions) Validate() error {
	if len(o.PodManifestDir) == 0 {
		return fmt.Errorf("--pod-manifest-dir is required")
	}

	return nil
}

func (o *GracefulMonitorOptions) Run() error {
	currentRevision := 0

	podPrefix := "kube-apiserver-pod"
	containerName := "kube-apiserver"
	return wait.PollImmediateInfinite(10, func() (bool, error) {
		manifests, err := ReadStaticPodManifests(o.PodManifestDir, podPrefix, containerName)
		if err != nil {
			klog.Errorf("Error reading static pod manifests: %v", err)
			return false, nil
		}

		activeManifest := StaticPodManifest{}
		nextManifest := StaticPodManifest{}
		nextRevision := 0
		switch len(manifests) {
		case 0:
			// TODO(marun) Cleanup chain
			klog.V(2).Infof("No static pod manifests found in path %q with prefix %q",
				o.PodManifestDir, podPrefix)
			return false, nil
		case 1:
			activeManifest = manifests[0]
			nextRevision = activeManifest.Revision
		case 2:
			if manifests[0].Revision < manifests[1].Revision {
				activeManifest = manifests[0]
				nextManifest = manifests[1]
			} else {
				activeManifest = manifests[1]
				nextManifest = manifests[0]
			}
			nextRevision = nextManifest.Revision
		default:
			klog.Errorf("Graceful transition only possible for 2 pods, but found %d.", len(manifests))
			return false, nil
		}

		if currentRevision == nextRevision {
			klog.V(7).Infof("Forwarding already configured for kube-apiserver %s.", activeManifest)
			return false, nil
		}

		err = gracefulRollout(activeManifest, nextManifest)
		if err != nil {
			klog.Errorf("Error attempting graceful rollout: %v", err)
		}

		currentRevision = nextRevision

		return false, nil
	})
}

func gracefulRollout(activeManifest, nextManifest StaticPodManifest) error {
	// TODO(marun) How to ensure support for ipv6?

	ipt, err := iptables.New()
	if err != nil {
		return err
	}

	activeMap := activePortMap(activeManifest.Port)

	// A pod on port 6443 does not need forwarding. This can occur if
	// enabling graceful rollout on an existing cluster.
	if activeManifest.Port == defaultSecurePort {
		klog.V(2).Infof("Ensuring no port forwarding for kube-apiserver %s", activeManifest)
		if err := removeChain(ipt); err != nil {
			return err
		}
	} else {
		klog.V(2).Infof("Ensuring port forwarding for kube-apiserver %s", activeManifest)
		if err := ensureActiveRules(ipt, activeMap); err != nil {
			return err
		}
	}
	if nextManifest.Invalid() {
		// No next pod to transition to.
		return nil
	}

	nextMap := NextPortMap(activeManifest.Port)

	if nextManifest.Port == defaultSecurePort {
		// If transitioning to the default port (i.e. due to disabling
		// graceful rollout), health checking the next apiserver won't
		// be possible since that port is currently forwarded to the
		// active apiserver.
		klog.V(2).Infof("Transitioning to kube-apiserver %s non-gracefully", nextManifest)
	} else {
		// Wait for the next pod to become ready by health checking
		// its insecure port.
		nextInsecurePort := nextMap[defaultInsecurePort]
		klog.V(2).Infof("Waiting for kube-apiserver %s to become ready", nextManifest)
		if err := waitForReadiness(nextManifest, nextInsecurePort); err != nil {
			return err
		}
		klog.V(2).Infof("kube-apiserver %s is ready", nextManifest)
	}

	// Remove the old pod's manifest
	klog.V(2).Infof("Initiating termination of kube-apiserver r%d by deleting manifest %s",
		activeManifest.Revision, activeManifest.Filename)
	if err := os.Remove(activeManifest.Filename); err != nil {
		if nestedErr := ensureActiveRules(ipt, activeMap); err != nil {
			klog.Errorf("Error attempting to cleanup forwarding rules: %v", nestedErr)
		}
		return err
	}

	// Ensure new connections are forwarded to the new pod
	if nextManifest.Port == defaultSecurePort {
		klog.V(2).Infof("Ensuring no port forwarding for kube-apiserver %s", nextManifest)
		return removeChain(ipt)
	} else {
		klog.V(2).Infof("Ensuring port forwarding for kube-apiserver %s", nextManifest)
		return ensureActiveRules(ipt, nextMap)
	}
}

// TODO(marun) Check pod readiness via the API in the installer instead?
func waitForReadiness(manifest StaticPodManifest, port int) error {
	url := fmt.Sprintf("http://localhost:%d/readyz", port)
	return wait.PollImmediate(1*time.Second, 3*time.Minute, func() (bool, error) {
		_, err := checkURL(url)
		if err != nil {
			// TODO(marun) Differentiate between error and timeout
			klog.Errorf("apiserver %s is not yet ready: %v", manifest, err)
		}
		return err == nil, nil
	})
}

// checkReadiness returns no error if a readyz request returns
// successfully.
func checkURL(url string) (string, error) {
	client := http.Client{
		Timeout: time.Second * 10,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	headers := http.Header{}
	headers.Set("User-Agent", fmt.Sprintf("graceful-monitor"))
	headers.Set("Accept", "*/*")
	req.Header = headers

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}

	// Closing idle connections ensures that golang will use a
	// different source port for subsequent requests which ensures
	// that the current iptables forwarding rule will be used instead
	// of the previously tracked connection to a different port.
	client.CloseIdleConnections()

	if res == nil {
		return "", errors.New("nil response")
	}

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	if res.StatusCode != 200 {
		return "", fmt.Errorf("expected 200 for %s, got %d: %s", url, res.StatusCode, body)
	}

	return string(body), nil
}

func activePortMap(activePort int) map[int]int {
	offset := activePort % defaultSecurePort
	return portMapForOffset(offset)
}

func NextPortMap(activePort int) map[int]int {
	// Next active port is 6444
	offset := 1
	if activePort == 6444 {
		// Next active port is 6445
		offset = 2
	}
	return portMapForOffset(offset)
}

func portMapForOffset(offset int) map[int]int {
	return map[int]int{
		defaultSecurePort:         defaultSecurePort + offset,
		defaultInsecurePort:       defaultInsecurePort + offset,
		defaultCheckEndpointsPort: defaultCheckEndpointsPort + offset,
	}
}
