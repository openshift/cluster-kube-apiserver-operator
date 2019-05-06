package watchdog

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
)

func TestPidObserver(t *testing.T) {
	var currentPIDMutex = sync.Mutex{}
	currentPID := 1

	getProcessPIDByName := func(name string) (int, bool, error) {
		currentPIDMutex.Lock()
		defer currentPIDMutex.Unlock()
		return currentPID, true, nil
	}

	watcher := &FileWatcherOptions{
		handleFindPidByNameFn: getProcessPIDByName,
	}

	pidObservedCh := make(chan int)
	monitorTerminated := make(chan struct{})

	go func() {
		defer close(monitorTerminated)
		watcher.startPidObserver(context.TODO(), pidObservedCh)
	}()

	// We should receive the initial PID
	select {
	case pid := <-pidObservedCh:
		if pid != 1 {
			t.Fatalf("expected PID 1, got %d", pid)
		}
		t.Log("initial PID observed")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout (observing initial PID)")
	}

	// We changed the PID, the monitor should gracefully terminate
	currentPIDMutex.Lock()
	currentPID = 10
	currentPIDMutex.Unlock()

	select {
	case <-monitorTerminated:
		t.Log("monitor successfully terminated")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout (terminating monitor)")
	}
}

func TestWatchdogRun(t *testing.T) {
	signalRecv := make(chan int)

	// Make temporary file we are going to watch and write changes
	testDir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testDir)
	if err := ioutil.WriteFile(filepath.Join(testDir, "testfile"), []byte("starting"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	opts := &FileWatcherOptions{
		ProcessName: "test",
		Files:       []string{filepath.Join(testDir, "testfile")},
		handleSignalFn: func(pid int) error {
			signalRecv <- pid
			return nil
		},
		handleFindPidByNameFn: func(name string) (int, bool, error) {
			return 10, true, nil
		},
		handleAddProcPrefixToFilesFn: func(files []string, i int) []string {
			return files
		},
		Interval: 200 * time.Millisecond,
		recorder: eventstesting.NewTestingEventRecorder(t),
	}

	// commandCtx is context used for the Run() method
	commandCtx, shutdown := context.WithTimeout(context.TODO(), 1*time.Minute)
	defer shutdown()

	commandTerminatedCh := make(chan struct{})
	go func() {
		defer close(commandTerminatedCh)
		if err := opts.Run(commandCtx); err != nil {
			t.Fatal(err)
		}
	}()

	// Give file watcher time to observe the file
	time.Sleep(1 * time.Second)

	// Modify the monitored file
	if err := ioutil.WriteFile(filepath.Join(testDir, "testfile"), []byte("changed"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	select {
	case pid := <-signalRecv:
		if pid != 10 {
			t.Errorf("expected received PID to be 10, got %d", pid)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout (waiting for PID)")
	}

	select {
	case <-commandTerminatedCh:
		t.Fatal("run command is not expected to terminate")
	default:
	}

	// Test the shutdown sequence
	shutdown()
	select {
	case <-commandTerminatedCh:
	case <-time.After(20 * time.Second):
		t.Fatal("run command failed to terminate")
	}

}
