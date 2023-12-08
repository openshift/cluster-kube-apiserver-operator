package podsecurityreadinesscontroller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	applyconfiguration "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	psapi "k8s.io/pod-security-admission/api"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	checkInterval = 240 * time.Minute // Adjust the interval as needed.
)

var podSecurityAlertLabels = []string{
	psapi.AuditLevelLabel,
	psapi.WarnLevelLabel,
}

// PodSecurityReadinessController checks if namespaces are ready for Pod Security Admission enforcement.
type PodSecurityReadinessController struct {
	kubeClient     kubernetes.Interface
	operatorClient v1helpers.OperatorClient

	warningsHandler   *warningsHandler
	namespaceSelector string
}

func NewPodSecurityReadinessController(
	kubeConfig *rest.Config,
	operatorClient v1helpers.OperatorClient,
	recorder events.Recorder,
) (factory.Controller, error) {
	warningsHandler := &warningsHandler{}

	kubeClientCopy := rest.CopyConfig(kubeConfig)
	kubeClientCopy.WarningHandler = warningsHandler
	// We don't want to overwhelm the apiserver with requests. On a cluster with
	// 10k namespaces, we would send 10k + 1 requests to the apiserver.
	kubeClientCopy.QPS = 2
	kubeClientCopy.Burst = 2
	kubeClient, err := kubernetes.NewForConfig(kubeClientCopy)
	if err != nil {
		return nil, err
	}

	selector := labels.NewSelector()
	labelsRequirement, err := labels.NewRequirement(psapi.EnforceLevelLabel, selection.DoesNotExist, []string{})
	if err != nil {
		return nil, err
	}

	c := &PodSecurityReadinessController{
		operatorClient:    operatorClient,
		kubeClient:        kubeClient,
		warningsHandler:   warningsHandler,
		namespaceSelector: selector.Add(*labelsRequirement).String(),
	}

	return factory.New().
		WithSync(c.sync).
		ResyncEvery(checkInterval).
		ToController("PodSecurityReadinessController", recorder), nil
}

func (c *PodSecurityReadinessController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	nsList, err := c.kubeClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: c.namespaceSelector})
	if err != nil {
		return err
	}

	conditions := podSecurityOperatorConditions{}
	for _, ns := range nsList.Items {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			isViolating, err := c.isNamespaceViolating(ctx, &ns)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if isViolating {
				conditions.addViolation(ns.Name)
			}

			return nil
		})
		if err != nil {
			klog.V(2).ErrorS(err, "namespace:", ns.Name)

			// We don't want to sync more often than the resync interval.
			return nil

		}
	}

	// We expect the Cluster's status conditions to be picked up by the status
	// controller and push it into the ClusterOperator's status, where it will
	// be evaluated by the ClusterFleetMechanic.
	_, _, err = v1helpers.UpdateStatus(ctx, c.operatorClient, conditions.toConditionFuncs()...)
	return err
}

func (c *PodSecurityReadinessController) isNamespaceViolating(ctx context.Context, ns *corev1.Namespace) (bool, error) {
	if ns.Labels[psapi.EnforceLevelLabel] != "" {
		// If someone has taken care of the enforce label, we don't need to
		// check for violations. Global Config nor PS-Label-Syncer will modify
		// it.
		return false, nil
	}

	targetLevel := ""
	for _, label := range podSecurityAlertLabels {
		levelStr, ok := ns.Labels[label]
		if !ok {
			continue
		}

		level, err := psapi.ParseLevel(levelStr)
		if err != nil {
			klog.V(4).InfoS("invalid level", "namespace", ns.Name, "level", levelStr)
			continue
		}

		if targetLevel == "" {
			targetLevel = levelStr
			continue
		}

		if psapi.CompareLevels(psapi.Level(targetLevel), level) < 0 {
			targetLevel = levelStr
		}
	}

	if targetLevel == "" {
		// Global Config will set it to "restricted".
		targetLevel = string(psapi.LevelRestricted)
	}

	nsApply := applyconfiguration.Namespace(ns.Name).WithLabels(map[string]string{
		psapi.EnforceLevelLabel: string(targetLevel),
	})

	_, err := c.kubeClient.CoreV1().
		Namespaces().
		Apply(ctx, nsApply, metav1.ApplyOptions{
			DryRun:       []string{metav1.DryRunAll},
			FieldManager: "pod-security-readiness-controller",
		})
	if err != nil {
		return false, err
	}

	// The information we want is in the warnings. It collects violations.
	warnings := c.warningsHandler.PopAll()

	return len(warnings) > 0, nil
}
