package certrotationcontroller

import (
	"time"

	"github.com/golang/glog"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
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
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "aggregator-client-signer",
			Validity:          1 * 8 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "managed-aggregator-client-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.TargetNamespace,
			Name:              "aggregator-client",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ClientRotation: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:openshift-aggregator"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
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
		"LocalhostServing",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "localhost-serving-signer",
			Validity:          10 * 365 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "localhost-serving-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.TargetNamespace,
			Name:              "localhost-serving-cert-certkey",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ServingRotation: &certrotation.ServingRotation{
				Hostnames: []string{"localhost", "127.0.0.1"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
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
		"ServiceNetworkServing",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "service-network-serving-signer",
			Validity:          10 * 365 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "service-network-serving-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.TargetNamespace,
			Name:              "service-network-serving-certkey",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ServingRotation: &certrotation.ServingRotation{
				// TODO, this needs to be late binding so we can keep it up to date as network configuration changes
				Hostnames: []string{"kubernetes", "kubernetes.default", "kubernetes.default.svc", "kubernetes.default.svc.cluster.local"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
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
		"LoadBalancerServing",
		certrotation.SigningRotation{
			Namespace:         operatorclient.OperatorNamespace,
			Name:              "loadbalancer-serving-signer",
			Validity:          10 * 365 * 24 * time.Hour,
			RefreshPercentage: 0.5,
			Informer:          kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:            kubeClient.CoreV1(),
			EventRecorder:     eventRecorder,
		},
		certrotation.CABundleRotation{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "loadbalancer-serving-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.TargetRotation{
			Namespace:         operatorclient.TargetNamespace,
			Name:              "loadbalancer-serving-serving-certkey",
			Validity:          1 * 4 * time.Hour,
			RefreshPercentage: 0.5,
			ServingRotation: &certrotation.ServingRotation{
				// TODO, this needs to be late binding so we can keep it up to date as network configuration changes
				Hostnames: []string{"api"}, // this isn't correct, but we need something to get certs started
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
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
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
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
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "managed-kube-apiserver-client-ca-bundle",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
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
	glog.Infof("Starting CertRotation")
	defer glog.Infof("Shutting down CertRotation")

	for _, certRotator := range c.certRotators {
		go certRotator.Run(workers, stopCh)
	}
}
