package node

import (
	"strconv"

	configv1 "github.com/openshift/api/config/v1"
	nodeobserver "github.com/openshift/library-go/pkg/operator/configobserver/node"
)

var LatencyConfigs = []nodeobserver.LatencyConfigProfileTuple{
	// default-not-ready-toleration-seconds: Default=300;Medium,Low=60
	{
		ConfigPath: []string{"apiServerArguments", "default-not-ready-toleration-seconds"},
		ProfileConfigValues: map[configv1.WorkerLatencyProfileType]string{
			configv1.DefaultUpdateDefaultReaction: strconv.Itoa(configv1.DefaultNotReadyTolerationSeconds),
			configv1.MediumUpdateAverageReaction:  strconv.Itoa(configv1.MediumNotReadyTolerationSeconds),
			configv1.LowUpdateSlowReaction:        strconv.Itoa(configv1.LowNotReadyTolerationSeconds),
		},
	},
	// default-unreachable-toleration-seconds: Default=300;Medium,Low=60
	{
		ConfigPath: []string{"apiServerArguments", "default-unreachable-toleration-seconds"},
		ProfileConfigValues: map[configv1.WorkerLatencyProfileType]string{
			configv1.DefaultUpdateDefaultReaction: strconv.Itoa(configv1.DefaultUnreachableTolerationSeconds),
			configv1.MediumUpdateAverageReaction:  strconv.Itoa(configv1.MediumUnreachableTolerationSeconds),
			configv1.LowUpdateSlowReaction:        strconv.Itoa(configv1.LowUnreachableTolerationSeconds),
		},
	},
}
