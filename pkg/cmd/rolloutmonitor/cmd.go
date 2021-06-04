package rolloutmonitor

import (
	"fmt"

	"github.com/davecgh/go-spew/spew"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
)

type RolloutMonitorOptions struct {
	PreviousPodRevision string
	NextPodRevision     string
}

func NewRolloutMonitorCommand() *cobra.Command {
	o := RolloutMonitorOptions{}

	cmd := &cobra.Command{
		Use:   "rollout-monitor",
		Short: "Monitors a new static pod revision and if it proves unhealthy rolls back to the previous revision.",
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

func (o *RolloutMonitorOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.PreviousPodRevision, "previous-pod-revision", "", "revision of the previous pod manifest")
	fs.StringVar(&o.NextPodRevision, "next-pod-revision", "", "revision of the next pod manifest")
}

func (o *RolloutMonitorOptions) Validate() error {
	if len(o.PreviousPodRevision) == 0 {
		return fmt.Errorf("--previous-pod-revision is required")
	}
	if len(o.NextPodRevision) == 0 {
		return fmt.Errorf("--next-pod-revision is required")
	}

	return nil
}

func (o *RolloutMonitorOptions) Run() error {
	// Watch the current revision
	// If it proves unhealthy, rollback to the previous revision

	// TODO How to determine if previous revision has been replaced by new revision?

	// TODO Determine if new revision is healthy

	// TODO If new revision is not healthy, revert to the previous pod manifest

	// TODO Remove rollout monitor manifest

	// TODO copy the contents of the previous pod filename to the manifest filename

	return nil
}
