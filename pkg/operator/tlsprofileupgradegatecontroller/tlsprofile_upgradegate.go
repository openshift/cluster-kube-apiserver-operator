package tlsprofileupgradegatecontroller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

const (
	adminGatesConfigMapName = "admin-gates"
	tlsProfileGateKey       = "ack-5.0-default-tls-profile-change-in-5.1"
	tlsProfileGateMessage   = "apiservers.config.openshift.io/cluster has no explicit " +
		"spec.tlsSecurityProfile set. In 5.1 the default TLS security profile changes " +
		"from Intermediate to Modern. Set an explicit spec.tlsSecurityProfile before " +
		"updating to 5.1, or acknowledge this change to accept the new Modern default."
)

// TLSProfileUpgradeGateController maintains an admin gate. This admin gate
// ensures that an administrator acks the fact that when upgrading off of v5.0
// the default TLS profile will change from Intermediate to Modern. If the
// admin has already chosen a TLS Profile moving forward the gate is removed.
type TLSProfileUpgradeGateController struct {
	client     corev1client.ConfigMapsGetter
	apiServers configv1listers.APIServerLister
}

func NewTLSProfileUpgradeGateController(
	client corev1client.ConfigMapsGetter,
	informer configv1informers.APIServerInformer,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TLSProfileUpgradeGateController{
		client:     client,
		apiServers: informer.Lister(),
	}

	recorder := eventRecorder.WithComponentSuffix("tlsprofile-upgrade-gate")

	return factory.New().
		WithInformers(informer.Informer()).
		WithSync(c.sync).
		ResyncEvery(5*time.Minute).
		ToController("TLSProfileUpgradeGateController", recorder)
}

func (c *TLSProfileUpgradeGateController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	config, err := c.apiServers.Get("cluster")
	if apierrors.IsNotFound(err) {
		config = &configv1.APIServer{}
	} else if err != nil {
		return fmt.Errorf("error fetching cluster api server config: %w", err)
	}

	ns := operatorclient.GlobalMachineSpecifiedConfigNamespace
	cm, err := c.client.ConfigMaps(ns).Get(ctx, adminGatesConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error fetching configmap %s during sync: %w", adminGatesConfigMapName, err)
	}

	cmcopy := cm.DeepCopy()
	noprofile := config.Spec.TLSSecurityProfile == nil
	updateGateKeyIfNeeded(noprofile, cmcopy)

	if reflect.DeepEqual(cm.Data, cmcopy.Data) {
		return nil
	}

	if _, err := c.client.ConfigMaps(ns).Update(ctx, cmcopy, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating configmap %s during sync: %w", adminGatesConfigMapName, err)
	}
	return nil
}

func updateGateKeyIfNeeded(noprofile bool, cm *corev1.ConfigMap) {
	if !noprofile {
		delete(cm.Data, tlsProfileGateKey)
		return
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[tlsProfileGateKey] = tlsProfileGateMessage
}
