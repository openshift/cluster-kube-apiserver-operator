package watchdog

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/openshift/library-go/pkg/config/client"
	"github.com/openshift/library-go/pkg/controller/fileobserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/retry"
)

type FileWatcherOptions struct {
	// ProcessName is the name of the process we will send SIGTERM
	ProcessName string

	// Files lists all files we want to monitor for changes
	Files      []string
	KubeConfig string

	// Namespace to report events to
	Namespace string
	recorder  events.Recorder

	// Interval specifies how aggressive we want to be in file checks
	Interval time.Duration

	// for unit-test to mock getting the process PID
	handleFindPidByNameFn func(name string) (int, bool, error)

	// for unit-test to mock sending UNIX signals
	handleSignalFn func(pid int) error

	// for unit-test to mock prefixing files (/proc/PID/root)
	handleAddProcPrefixToFilesFn func([]string, int) []string
}

func NewFileWatcherOptions() *FileWatcherOptions {
	return &FileWatcherOptions{
		handleFindPidByNameFn:        FindPidByName,
		handleAddProcPrefixToFilesFn: addProcPrefixToFiles,
		handleSignalFn: func(pid int) error {
			return syscall.Kill(pid, syscall.SIGTERM)
		},
	}
}

// NewFileWatcherWatchdog return the file watcher watchdog command.
func NewFileWatcherWatchdog() *cobra.Command {
	o := NewFileWatcherOptions()

	cmd := &cobra.Command{
		Use:   "file-watcher-watchdog",
		Short: "Watch files on the disk and kill the specified process on change",
		Run: func(cmd *cobra.Command, args []string) {
			klog.V(1).Info(cmd.Flags())
			klog.V(1).Info(spew.Sdump(o))

			// Handle shutdown
			termHandler := server.SetupSignalHandler()
			ctx, shutdown := context.WithCancel(context.TODO())
			go func() {
				defer shutdown()
				<-termHandler
			}()

			if err := o.Complete(); err != nil {
				klog.Fatal(err)
			}
			if err := o.Validate(); err != nil {
				klog.Fatal(err)
			}

			if err := o.Run(ctx); err != nil {
				klog.Fatal(err)
			}
		},
	}

	o.AddFlags(cmd.Flags())

	return cmd
}

func (o *FileWatcherOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.ProcessName, "process-name", "", "name of the process to send signal to on file change (eg. 'hyperkube').")
	fs.StringSliceVar(&o.Files, "files", o.Files, "comma separated list of file names to monitor for changes")
	fs.StringVar(&o.KubeConfig, "kubeconfig", o.KubeConfig, "kubeconfig file or empty")
	fs.StringVar(&o.Namespace, "namespace", o.Namespace, "namespace to report the watchdog events")
	fs.DurationVar(&o.Interval, "interval", 5*time.Second, "interval specifying how aggressive the file checks should be")
}

func (o *FileWatcherOptions) Complete() error {
	clientConfig, err := client.GetKubeConfigOrInClusterConfig(o.KubeConfig, nil)
	if err != nil {
		return err
	}
	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	// Get event recorder.
	// Retry on connection errors for 10s, but don't error out, instead fallback to the namespace.
	var eventTarget *v1.ObjectReference
	err = retry.RetryOnConnectionErrors(ctx, func(context.Context) (bool, error) {
		var clientErr error
		eventTarget, clientErr = events.GetControllerReferenceForCurrentPod(kubeClient, o.Namespace, nil)
		if clientErr != nil {
			return false, clientErr
		}
		return true, nil
	})
	if err != nil {
		klog.Warningf("unable to get owner reference (falling back to namespace): %v", err)
	}
	o.recorder = events.NewRecorder(kubeClient.CoreV1().Events(o.Namespace), "file-change-watchdog", eventTarget)

	return nil
}

func (o *FileWatcherOptions) Validate() error {
	if len(o.ProcessName) == 0 {
		return fmt.Errorf("process name must be specified")
	}
	if len(o.Files) == 0 {
		return fmt.Errorf("at least one file to observe must be specified")
	}
	if len(o.Namespace) == 0 && len(os.Getenv("POD_NAMESPACE")) == 0 {
		return fmt.Errorf("either namespace flag or POD_NAMESPACE environment variable must be specified")
	}
	return nil
}

// startPidObserver runs a loop that observes changes to the PID of the process we send signals after change is detected.
// When the PID is observed for the first time, the pidObservedCh is closed.
// When the PID change is observed (after we got initial PID), the shutdown is called (to signal watchdog to restart).
func (o *FileWatcherOptions) startPidObserver(ctx context.Context, pidObservedCh chan int) {
	defer close(pidObservedCh)
	currentPID := 0
	retries := 0
	pollErr := wait.PollImmediateUntil(1*time.Second, func() (done bool, err error) {
		retries++
		// get the monitored process PID
		observedPID, found, err := o.handleFindPidByNameFn(o.ProcessName)
		if !found || err != nil {
			klog.Warningf("Unable to determine PID for %q (retry: %d, err: %v)", o.ProcessName, retries, err)
			return false, nil
		}

		if currentPID == 0 {
			currentPID = observedPID
			pidObservedCh <- observedPID
		}

		// when the PID change, signal shutdown
		if currentPID != observedPID {
			return true, nil
		}

		return false, nil
	}, ctx.Done())

	// These are not fatal
	if pollErr != nil {
		klog.Warningf("PID observer error: %v", pollErr)
	}
}

// readInitialFileContent reads the content of files specified.
// This is needed by file observer.
func readInitialFileContent(files []string) (map[string][]byte, error) {
	initialContent := map[string][]byte{}
	for _, name := range files {
		// skip files that does not exists yet
		if _, err := os.Stat(name); os.IsNotExist(err) {
			continue
		}
		content, err := ioutil.ReadFile(name)
		if err != nil {
			return nil, err
		}
		initialContent[name] = content
	}
	return initialContent, nil
}

// addProcPrefixToFiles mutates the file list and prefix every file with /proc/PID/root.
func addProcPrefixToFiles(oldFiles []string, pid int) []string {
	files := []string{}
	for _, file := range oldFiles {
		files = append(files, filepath.Join("/proc", fmt.Sprintf("%d", pid), "root", file))
	}
	return files
}

// Run starts the main watchdog loop.
func (o *FileWatcherOptions) Run(ctx context.Context) error {
	for {
		{
			instanceCtx, shutdown := context.WithCancel(ctx)
			defer shutdown()
			select {
			case <-ctx.Done():
				shutdown()
				return nil
			default:
			}

			if err := o.runWatchdog(instanceCtx); err != nil {
				return err
			}
		}
	}
}

func (o *FileWatcherOptions) runWatchdog(ctx context.Context) error {
	watchdogCtx, shutdown := context.WithCancel(ctx)
	defer shutdown()

	// Handle watchdog shutdown
	go func() {
		defer shutdown()
		<-ctx.Done()
	}()

	pidObservedCh := make(chan int)
	go o.startPidObserver(watchdogCtx, pidObservedCh)

	// Wait while we get the initial PID for the process
	klog.Infof("Waiting for process %q PID ...", o.ProcessName)
	currentPID := <-pidObservedCh

	// Mutate path for specified files as '/proc/PID/root/<original path>'
	// This means side-car container don't have to duplicate the mounts from main container.
	// This require shared PID namespace feature.
	filesToWatch := o.handleAddProcPrefixToFilesFn(o.Files, currentPID)
	klog.Infof("Watching for changes in: %s", spew.Sdump(filesToWatch))

	// Read initial file content. If shared PID namespace does not work, this will error.
	initialContent, err := readInitialFileContent(filesToWatch)
	if err != nil {
		o.recorder.Warningf("FileChangeWatchdogFailed", "Reading initial file content failed: %v", err)
		return fmt.Errorf("unable to read initial file content: %v", err)
	}

	o.recorder.Eventf("FileChangeWatchdogStarted", "Started watching files for process %s[%d]", o.ProcessName, currentPID)

	observer, err := fileobserver.NewObserver(o.Interval)
	if err != nil {
		o.recorder.Warningf("ObserverFailed", "Failed to start to file observer: %v", err)
		klog.Fatalf("unable to start file observer: %v", err)
	}

	observer.AddReactor(func(file string, action fileobserver.ActionType) error {
		o.recorder.Eventf("FileChangeObserved", "Observed change in file %q, sending SIGINT to %s[%d]", file, o.ProcessName, currentPID)
		retries := 0
		// When file change is observed, don't give up on errors when we fail to send signal, but retry sending
		// and fail hard if we fail to deliver it. Try 5 times, sleep 1s between attempts.
		return wait.PollImmediate(1*time.Second, 5*time.Second, func() (done bool, err error) {
			defer func() {
				klog.Info("TERM signal dispatched, shutting down watchdog ...")
				shutdown()
			}()
			if err := o.handleSignalFn(currentPID); err != nil {
				retries++
				// NOTE: Failing to deliver signal means we failed to reload process. This is critical and might lead
				//       to a process that use out-dated certificates.
				o.recorder.Warningf("SignalFailed", "Failed to send signal to %s[%d] (retry #%d): %v", o.ProcessName, currentPID, retries, err)
				return false, nil
			}
			return true, nil
		})
	}, initialContent, filesToWatch...)

	go observer.Run(watchdogCtx.Done())

	<-watchdogCtx.Done()
	return nil
}
