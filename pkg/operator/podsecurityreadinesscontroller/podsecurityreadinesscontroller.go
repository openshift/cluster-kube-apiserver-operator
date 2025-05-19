package podsecurityreadinesscontroller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
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

	kubeClient, err := newWarningAwareKubeClient(warningsHandler, kubeConfig)
	if err != nil {
		return nil, err
	}

	selector, err := nonEnforcingSelector()
	if err != nil {
		return nil, err
	}

	c := &PodSecurityReadinessController{
		operatorClient:    operatorClient,
		kubeClient:        kubeClient,
		warningsHandler:   warningsHandler,
		namespaceSelector: selector,
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
				conditions.addViolation(&ns)
			}

			return nil
		})
		if err != nil {
			klog.V(2).ErrorS(err, "namespace:", ns.Name)

			conditions.addInconclusive(&ns)
		}
	}

	// We expect the Cluster's status conditions to be picked up by the status
	// controller and push it into the ClusterOperator's status, where it will
	// be evaluated by the ClusterFleetMechanic.
	_, _, err = v1helpers.UpdateStatus(ctx, c.operatorClient, conditions.toConditionFuncs()...)
	return err
}

func nonEnforcingSelector() (string, error) {
	selector := labels.NewSelector()
	labelsRequirement, err := labels.NewRequirement(psapi.EnforceLevelLabel, selection.DoesNotExist, []string{})
	if err != nil {
		return "", err
	}

	return selector.Add(*labelsRequirement).String(), nil
}

func newWarningAwareKubeClient(warningsHandler *warningsHandler, kubeConfig *rest.Config) (*kubernetes.Clientset, error) {
	kubeClientCopy := rest.CopyConfig(kubeConfig)
	kubeClientCopy.WarningHandler = warningsHandler
	// We don't want to overwhelm the apiserver with requests. On a cluster with
	// 10k namespaces, we would send 10k + 1 requests to the apiserver.
	kubeClientCopy.QPS = 2
	kubeClientCopy.Burst = 2

	return kubernetes.NewForConfig(kubeClientCopy)
}
