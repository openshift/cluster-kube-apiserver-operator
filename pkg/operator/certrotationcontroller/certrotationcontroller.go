package certrotationcontroller

import (
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/resources"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"k8s.io/client-go/kubernetes"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"k8s.io/apiserver/pkg/authentication/user"
)

type CertRotationController struct {
	certRotators []*certrotation.CertRotationController
}

func NewCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
) (*CertRotationController, error) {
	ret := &CertRotationController{}

	certRotator, err := certrotation.NewCertRotationController(
		"AggregatorProxyClientCert",
		resources.AggregatorProxyClientCert,
		kubeClient.CoreV1(),
		kubeInformersForNamespaces,
		eventRecorder,
		operatorClient,
	)
	if err != nil {
		return nil, err
	}
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator, err = certrotation.NewCertRotationController(
		"KubeControllerManagerClient",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-client-signer",
			Validity:          1 * 8 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:              "kube-controller-manager-client-cert-key",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-controller-manager"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		operatorClient,
	)
	if err != nil {
		return nil, err
	}
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator, err = certrotation.NewCertRotationController(
		"KubeSchedulerClient",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-client-signer",
			Validity:          1 * 8 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:              "kube-scheduler-client-cert-key",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-scheduler"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		operatorClient,
	)
	if err != nil {
		return nil, err
	}
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator, err = certrotation.NewCertRotationController(
		"ManagedKubeAPIServerServingCert",
		certrotation.SigningRotation{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "managed-kube-apiserver-serving-cert-signer",
			// this is super long because we have no auto-refresh consuming new values from ca.crt inside the cluster
			Validity:          365 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "managed-kube-apiserver-serving-cert-ca-bundle",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "managed-kube-apiserver-serving-cert-key",
			// this is comparatively short because we can rotate what we use to serve without forcing a rotation in trust itself
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ServingRotation: &certrotation.ServingRotation{
				Hostnames: []string{"localhost", "127.0.0.1", "kubernetes.default.svc"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
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
