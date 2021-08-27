package deadman

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/util/wait"
)

type Heartbeat struct {
	file     string
	interval time.Duration

	// maxDeadDuration represents a duration to tolerate between last stored heartbeat timestamp and current time.
	// if the duration is exceeded the callbackFn is being triggered.
	// the callbackFn is triggered only when this process starts.
	maxDeadDuration time.Duration
	callbackFn      func(ctx context.Context) error
}

func (h *Heartbeat) Run(ctx context.Context) error {
	var lastRecordedHeartbeat int64

	lastRecordedHeartbeatBytes, err := ioutil.ReadFile(h.file)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unable to read existing heartbeat file %q: %v", h.file, err)
	}

	if err == nil && len(lastRecordedHeartbeatBytes) > 0 {
		i, err := strconv.Atoi(string(lastRecordedHeartbeatBytes))
		if err != nil {
			return err
		}
		lastRecordedHeartbeat = int64(i)
	} else {
		// the file does not exists or there is no timestamp recorded in it
		lastRecordedHeartbeat = time.Now().Unix()
	}

	// if last recorded heartbeat is out of tolerated duration, trigger callback function
	if d := time.Now().Sub(time.Unix(lastRecordedHeartbeat, 0)); d > h.maxDeadDuration {
		klog.Infof("Triggering callback function because difference between last heart beat is %s", d)
		go func(ctx context.Context) {
			n := time.Now()
			defer func() {
				klog.Infof("Callback function finished in %s", time.Now().Sub(n))
			}()
			if err := h.callbackFn(ctx); err != nil {
				klog.Errorf("Callback function failed: %v", err)
			}
		}(ctx)
	}

	// keep writing timestamp to file until context is cancelled
	wait.Forever(func() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		currentHeartbeat := time.Now().Unix()
		if err := ioutil.WriteFile(h.file, []byte(fmt.Sprintf("%d", currentHeartbeat)), os.ModePerm); err != nil {
			klog.Fatalf("Unable to write timestamp file %q: %v", h.file, err)
		}
	}, h.interval)
	return nil
}
