package certrotationcontroller

import (
	"time"

	"k8s.io/klog"

	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

type CertRotationController struct {
	certRotators []*certrotation.CertRotationController
}

func NewCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	ret := &CertRotationController{}

	rotationDay := defaultRotationDay
	if day != time.Duration(0) {
		rotationDay = day
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", rotationDay)
	}

	certRotator, err := certrotation.NewCertRotationController(
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
		operatorClient,
	)
	if err != nil {
		return nil, err
	}
	ret.certRotators = append(ret.certRotators, certRotator)

	return ret, nil
}

func (c *CertRotationController) Run(workers int, stopCh <-chan struct{}) {
	for _, certRotator := range c.certRotators {
		go certRotator.Run(workers, stopCh)
	}
}
