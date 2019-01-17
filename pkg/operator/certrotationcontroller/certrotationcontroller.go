package certrotationcontroller

import (
	"time"

	"github.com/openshift/library-go/pkg/operator/events"

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
	kubeInformersForNamespaces operatorclient.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
) *CertRotationController {
	ret := &CertRotationController{}

	ret.certRotators = append(ret.certRotators, certrotation.NewCertRotationController(
		"AggregatorProxyClientCert",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "aggregator-client-signer",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.MachineSpecifiedGlobalConfigNamespace,
			Name:          "aggregator-client-ca",
			Informer:      kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.TargetNamespaceName,
			Name:              "aggregator-client",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.75,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:openshift-aggregator"},
			},
			Informer:      kubeInformersForNamespaces[operatorclient.TargetNamespaceName].Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces[operatorclient.TargetNamespaceName].Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
	))

	ret.certRotators = append(ret.certRotators, certrotation.NewCertRotationController(
		"KubeControllerManagerClient",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-client-signer",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.MachineSpecifiedGlobalConfigNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.MachineSpecifiedGlobalConfigNamespace,
			Name:              "kube-controller-manager-client-cert-key",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.75,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-controller-manager"},
			},
			Informer:      kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
	))

	ret.certRotators = append(ret.certRotators, certrotation.NewCertRotationController(
		"KubeSchedulerClient",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-client-signer",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.MachineSpecifiedGlobalConfigNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.MachineSpecifiedGlobalConfigNamespace,
			Name:              "kube-controller-manager-client-cert-key",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.75,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-scheduler"},
			},
			Informer:      kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces[operatorclient.MachineSpecifiedGlobalConfigNamespace].Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
	))

	ret.certRotators = append(ret.certRotators, certrotation.NewCertRotationController(
		"ManagedKubeAPIServerServingCert",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-serving-cert-signer",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "managed-kube-apiserver-serving-cert-ca-bundle",
			Informer:      kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "managed-kube-apiserver-serving-cert-key",
			Validity:          1 * 24 * time.Hour,
			RefreshPercentage: 0.75,
			ServingRotation: &certrotation.ServingRotation{
				Hostnames: []string{"localhost", "127.0.0.1", "kubernetes.default.svc"},
			},
			Informer:      kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces[operatorclient.OperatorNamespace].Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
	))

	return ret
}

func (c *CertRotationController) Run(workers int, stopCh <-chan struct{}) {
	for _, certRotator := range c.certRotators {
		go certRotator.Run(workers, stopCh)
	}
}
