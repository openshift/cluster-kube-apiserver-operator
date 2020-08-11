package certrotationcontroller

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/regeneratecerts/carry/kubecontrollermanager/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

type CertRotationController struct {
	certRotators []factory.Controller
	cachesToSync []cache.InformerSynced
	recorder     events.Recorder
}

func NewCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	ret := &CertRotationController{
		// we have to wait for the informers to be synced because of RunOnce...
		cachesToSync: []cache.InformerSynced{
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Informer().HasSynced,
		},
	}

	rotationDay := defaultRotationDay
	if day != time.Duration(0) {
		rotationDay = day
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", rotationDay)
	}

	certRotator := certrotation.NewCertRotationController(
		"CSRSigningCert",
		certrotation.SigningRotation{
			Namespace: operatorclient.OperatorNamespace,
			// this is not a typo, this is the signer of the signer
			Name:          "csr-signer-signer",
			Validity:      60 * rotationDay,
			Refresh:       30 * rotationDay,
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "csr-controller-signer-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        configMapsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "csr-signer",
			Validity:  30 * rotationDay,
			Refresh:   15 * rotationDay,
			CertCreator: &certrotation.SignerRotation{
				SignerName: "kube-csr-signer",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		nil,
		eventRecorder,
	)
	ret.recorder = eventRecorder
	ret.certRotators = append(ret.certRotators, certRotator)

	return ret, nil
}

func (c *CertRotationController) WaitForReady(stopCh <-chan struct{}) {
	klog.Infof("Waiting for CertRotation")
	defer klog.Infof("Finished waiting for CertRotation")

	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}
}

// RunOnce will run the cert rotation logic, but will not try to update the static pod status.
// This eliminates the need to pass an OperatorClient and avoids dubious writes and status.
func (c *CertRotationController) RunOnce() error {
	errlist := []error{}
	runOnceCtx := context.WithValue(context.Background(), certrotation.RunOnceContextKey, true)
	for _, certRotator := range c.certRotators {
		if err := certRotator.Sync(runOnceCtx, factory.NewSyncContext("CertRotationController", c.recorder)); err != nil {
			errlist = append(errlist, err)
		}
	}

	return utilerrors.NewAggregate(errlist)
}
