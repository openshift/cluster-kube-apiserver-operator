package deadman

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	"k8s.io/component-base/version"

	configv1 "github.com/openshift/api/config/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
	heartBeatFile     string
}

func NewDeadmanCommand(ctx context.Context) *cobra.Command {
	o := &Options{}

	ccc := controllercmd.NewControllerCommandConfig("deadman", version.Get(), func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
		o.controllerContext = controllerContext

		err := o.Validate(ctx)
		if err != nil {
			return err
		}

		err = o.Complete(ctx)
		if err != nil {
			return err
		}

		err = o.Run(ctx)
		if err != nil {
			return err
		}

		return nil
	})

	ccc.DisableServing = true
	cmd := ccc.NewCommandWithContext(ctx)
	cmd.Use = "deadman"
	cmd.Short = "Reset cluster operators status to unknown after cluster shutdown"

	o.AddFlags(cmd.Flags())

	return cmd
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.heartBeatFile, "heartbeat-file", o.heartBeatFile, "The file to use to store the last heart beat timestamp")
}

func (o *Options) Validate(ctx context.Context) error {
	return nil
}

func (o *Options) Complete(ctx context.Context) error {
	return nil
}

func (o *Options) Run(ctx context.Context) error {
	configClient, err := configeversionedclient.NewForConfig(o.controllerContext.KubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	return (&Heartbeat{
		file:            o.heartBeatFile,
		interval:        1 * time.Minute,
		maxDeadDuration: 60 * time.Minute,
		callbackFn: func(ctx context.Context) error {
			clusteroperators, err := configClient.ConfigV1().ClusterOperators().List(ctx, v1.ListOptions{})
			if err != nil {
				return fmt.Errorf("unable to list cluster operator: %v", err)
			}
			clusterOperatorsToUpdate := []*configv1.ClusterOperator{}
			for _, clusterOperator := range clusteroperators.Items {
				needUpdate := false
				hasTransition := false
				operator := clusterOperator.DeepCopy()
				for i, c := range clusterOperator.Status.Conditions {
					if c.Status == configv1.ConditionUnknown {
						continue
					}
					if time.Now().Sub(c.LastTransitionTime.Time) < 30*time.Minute {
						hasTransition = true
						continue
					}
					operator.Status.Conditions[i].Status = configv1.ConditionUnknown
					operator.Status.Conditions[i].Reason = "WaitingForOperatorUpdate"
					operator.Status.Conditions[i].Message = "Waiting for operator to provide current operand status"
					needUpdate = true
				}
				// operator need update only if there is condition that is not in unknown state and the operator has not made any progress
				// for last 30 minutes.
				if needUpdate && !hasTransition {
					clusterOperatorsToUpdate = append(clusterOperatorsToUpdate, operator)
				}
			}
			var errors []error
			for i := range clusterOperatorsToUpdate {
				err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
					_, updateErr := configClient.ConfigV1().ClusterOperators().Update(ctx, clusterOperatorsToUpdate[i], v1.UpdateOptions{})
					return updateErr
				})
				if err != nil {
					errors = append(errors, err)
				}
			}
			return errutil.NewAggregate(errors)
		},
	}).Run(ctx)
}
