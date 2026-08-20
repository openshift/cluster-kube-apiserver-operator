package certregenerationcontroller

import (
	"context"
	"time"

	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
)

// caBundleController composes individual certs into CA bundle that is used
// by kube-apiserver to validate clients.
// Cert recovery refreshes "kube-control-plane-signer-ca" and needs the containing
// bundle regenerated so kube-controller-manager and kube-scheduler can connect
// using client certs.
type caBundleController struct {
	configMapGetter corev1client.ConfigMapsGetter
	configMapLister corev1listers.ConfigMapLister
	eventRecorder   events.Recorder
}

func NewCABundleController(
	configMapGetter corev1client.ConfigMapsGetter,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &caBundleController{
		configMapGetter: configMapGetter,
		configMapLister: kubeInformersForNamespaces.ConfigMapLister(),
		eventRecorder:   eventRecorder.WithComponentSuffix("manage-client-ca-bundle-recovery-controller"),
	}

	namespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	}
	var informers []factory.Informer
	for _, ns := range namespaces {
		informers = append(informers, kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer())
	}

	return factory.New().
		WithInformers(informers...).
		WithSync(c.sync).
		ToController("CABundleRecoveryController", c.eventRecorder)
}

func (c *caBundleController) sync(ctx context.Context, _ factory.SyncContext) error {
	// Always start 10 seconds later after a change occurred. Makes us less likely to steal work and logs from the operator.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil
	}

	_, changed, err := targetconfigcontroller.ManageClientCABundle(ctx, c.configMapLister, c.configMapGetter, c.eventRecorder)
	if err != nil {
		return err
	}

	if changed {
		klog.V(2).Info("Refreshed client CA bundle.")
	}

	return nil
}
