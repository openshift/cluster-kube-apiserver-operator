package deadman

import (
	"context"
	"fmt"
	"time"

	configv1informers "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/component-base/version"

	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext

	challengeInterval             time.Duration
	controllerResponseGracePeriod time.Duration
}

func NewDeadmanCommand(ctx context.Context) *cobra.Command {
	o := &Options{
		challengeInterval:             1 * time.Minute,
		controllerResponseGracePeriod: 1 * time.Minute,
	}

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

	// have to disable serving because we do not have an open port on the master hosts.
	ccc.DisableServing = true
	cmd := ccc.NewCommandWithContext(ctx)
	cmd.Use = "deadman"
	cmd.Short = "Reset cluster operators status to Unknown if they do not actively manage status."

	o.AddFlags(cmd.Flags())

	return cmd
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.challengeInterval, "challenge-interval", o.challengeInterval, "Duration in between condition message resets")
	fs.DurationVar(&o.controllerResponseGracePeriod, "controller-response-grace-period", o.controllerResponseGracePeriod, "Duration after challenging in which the operator can fix status. Past this duration, the operator is considered dead.")
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
	configInformers := configv1informers.NewSharedInformerFactory(configClient, 1*time.Minute)

	conditionChallenger := NewOperatorConditionChallenger(configClient, configInformers, o.challengeInterval, o.controllerContext.EventRecorder)
	stalenessChecker := NewOperatorStalenessChecker(configClient, configInformers, o.controllerResponseGracePeriod, o.controllerContext.EventRecorder)

	go conditionChallenger.Run(ctx, 5)
	go stalenessChecker.Run(ctx, 5)
	go configInformers.Start(ctx.Done())

	<-ctx.Done()
	return nil
}
