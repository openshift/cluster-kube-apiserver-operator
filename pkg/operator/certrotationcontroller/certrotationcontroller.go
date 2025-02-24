package certrotationcontroller

import (
	"context"
	"fmt"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisterv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

type CertRotationController struct {
	certRotators []factory.Controller

	networkLister        configlisterv1.NetworkLister
	infrastructureLister configlisterv1.InfrastructureLister

	serviceNetwork        *DynamicServingRotation
	serviceHostnamesQueue workqueue.RateLimitingInterface

	externalLoadBalancer               *DynamicServingRotation
	externalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	internalLoadBalancer               *DynamicServingRotation
	internalLoadBalancerHostnamesQueue workqueue.RateLimitingInterface

	recorder events.Recorder

	cachesToSync []cache.InformerSynced
}

func NewCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		day,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		day,
		true,
	)
}

func newCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
	refreshOnlyWhenExpired bool,
) (*CertRotationController, error) {
	ret := &CertRotationController{
		networkLister:        configInformer.Config().V1().Networks().Lister(),
		infrastructureLister: configInformer.Config().V1().Infrastructures().Lister(),

		serviceHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ServiceHostnames"),
		serviceNetwork:        &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		externalLoadBalancerHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ExternalLoadBalancerHostnames"),
		externalLoadBalancer:               &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		internalLoadBalancerHostnamesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "InternalLoadBalancerHostnames"),
		internalLoadBalancer:               &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},

		recorder: eventRecorder,
		cachesToSync: []cache.InformerSynced{
			configInformer.Config().V1().Networks().Informer().HasSynced,
			configInformer.Config().V1().Infrastructures().Informer().HasSynced,
		},
	}

	configInformer.Config().V1().Networks().Informer().AddEventHandler(ret.serviceHostnameEventHandler())
	configInformer.Config().V1().Infrastructures().Informer().AddEventHandler(ret.externalLoadBalancerHostnameEventHandler())

	rotationDay := defaultRotationDay
	if day != time.Duration(0) {
		rotationDay = day
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", rotationDay)
	} else {
		// for the development cycle, make the rotation 60 times faster (every twelve hours or so).
		// This must be reverted before we ship
		rotationDay = rotationDay / 60
	}

	certRotator := certrotation.NewCertRotationController(
		"AggregatorProxyClientCert",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "aggregator-client-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test would ensure kube-apiserver request would be passed to openshift-apiserver
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]'",
				Description:                      "Signer for the kube-apiserver to create client certificates for aggregated apiservers to recognize as a front-proxy.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:      "kube-apiserver-aggregator-client-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test would ensure kube-apiserver request would be passed to openshift-apiserver
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]'",
				Description:                      "CA for aggregated apiservers to recognize kube-apiserver as front-proxy.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "aggregator-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test would ensure kube-apiserver request would be passed to openshift-apiserver
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]'",
				Description:                      "Client certificate used by the kube-apiserver to communicate to aggregated apiservers.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:openshift-aggregator"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeAPIServerToKubeletClientCert",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-apiserver-to-kubelet-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test ensures kube-apiserver can contact kubelets
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Signer for the kube-apiserver-to-kubelet-client so kubelets can recognize the kube-apiserver.",
			},
			Validity: 1 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			Refresh:                292 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-apiserver-to-kubelet-client-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test ensures kube-apiserver can contact kubelets
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "CA for the kubelet to recognize the kube-apiserver client certificate.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "kubelet-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test ensures kube-apiserver can contact kubelets
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Client certificate used by the kube-apiserver to authenticate to the kubelet for requests like exec and logs.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-apiserver", Groups: []string{"kube-master"}},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"LocalhostServing",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "localhost-serving-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via localhost.",
			},
			Validity: 10 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                8 * 365 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "localhost-serving-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for recognizing the kube-apiserver when connecting via localhost.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "localhost-serving-cert-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via localhost.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ServiceNetworkServing",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "service-network-serving-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
			},
			Validity: 10 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                8 * 365 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "service-network-serving-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for recognizing the kube-apiserver when connecting via the service network (kuberentes.default.svc).",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "service-network-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via service network
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the service network.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames:        ret.serviceNetwork.GetHostnames,
				HostnamesChanged: ret.serviceNetwork.hostnamesChanged,
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ExternalLoadBalancerServing",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "loadbalancer-serving-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
			},
			Validity: 10 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                8 * 365 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "loadbalancer-serving-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "external-loadbalancer-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver requests can be made via external LB
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the external load balancer.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames:        ret.externalLoadBalancer.GetHostnames,
				HostnamesChanged: ret.externalLoadBalancer.hostnamesChanged,
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"InternalLoadBalancerServing",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "loadbalancer-serving-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint  [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
			},
			Validity: 10 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                8 * 365 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "loadbalancer-serving-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint  [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "internal-loadbalancer-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint  [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the internal load balancer.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames:        ret.internalLoadBalancer.GetHostnames,
				HostnamesChanged: ret.internalLoadBalancer.hostnamesChanged,
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"LocalhostRecoveryServing",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "localhost-recovery-serving-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
			},
			Validity: 10 * 365 * defaultRotationDay, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:       8 * 365 * defaultRotationDay,
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "localhost-recovery-serving-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for recognizing the kube-apiserver when connecting via the localhost recovery SNI ServerName.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "localhost-recovery-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the localhost recovery SNI ServerName.",
			},
			Validity: 10 * 365 * defaultRotationDay,
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh: 8 * 365 * defaultRotationDay,
			CertCreator: &certrotation.ServingRotation{
				Hostnames: func() []string { return []string{"localhost-recovery"} },
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeControllerManagerClient",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-controller-manager can contact kube-apiserver and create pods for deployments
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
			},
			Validity:               60 * defaultRotationDay,
			Refresh:                30 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-controller-manager can contact kube-apiserver and create pods for deployments
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:      "kube-controller-manager-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-controller-manager can contact kube-apiserver and create pods for deployments
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Client certificate used by the kube-controller-manager to authenticate to the kube-apiserver.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-controller-manager"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeSchedulerClient",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-scheduler can contact kube-apiserver and pick nodes for pods
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
			},
			Validity:               60 * defaultRotationDay,
			Refresh:                30 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-scheduler can contact kube-apiserver and pick nodes for pods
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:      "kube-scheduler-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-scheduler can contact kube-apiserver and pick nodes for pods
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Client certificate used by the kube-scheduler to authenticate to the kube-apiserver.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:kube-scheduler"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ControlPlaneNodeAdminClient",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ControlPlaneNodeAdminClient signer is not being tested directly, but this signer needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
			},
			Validity:               60 * defaultRotationDay,
			Refresh:                30 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// ControlPlaneNodeAdminClient is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "control-plane-node-admin-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via control-plane-node kubeconfig
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"control-plane-node.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]'",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:control-plane-node-admin", Groups: []string{"system:masters"}},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"CheckEndpointsClient",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// CheckEndpointsClient signer is not being tested directly, but this signer needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
			},
			Validity:               60 * defaultRotationDay,
			Refresh:                30 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "kube-control-plane-signer-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// CheckEndpointsClient CA is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "check-endpoints-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via check-endpoints kubeconfig
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"check-endpoints.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Client certificate used by the network connectivity checker of the kube-apiserver.",
			},
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: "system:serviceaccount:openshift-kube-apiserver:check-endpoints"},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"NodeSystemAdminClient",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "node-system-admin-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// NodeSystemAdminClient CA is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Signer for the per-master-debugging-client.",
			},
			Validity: 1 * 365 * defaultRotationDay,
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			Refresh:                292 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "node-system-admin-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// NodeSystemAdminClient CA is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "CA for kube-apiserver to recognize local system:masters rendered to each master.",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "node-system-admin-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]'",
				Description:                      "Client certificate (system:masters) placed on each master to allow communication to kube-apiserver for debugging.",
			},
			// This needs to live longer then control plane certs so there is high chance that if a cluster breaks
			// because of expired certs these are still valid to use for collecting data using localhost-recovery
			// endpoint with long lived serving certs for localhost.
			Validity: 2 * 365 * defaultRotationDay,
			// We rotate sooner so certs are always valid for 90 days (30 days more then kube-control-plane-signer)
			Refresh:                30 * defaultRotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{
					Name:   "system:admin",
					Groups: []string{"system:masters"},
				},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
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

	// need to sync at least once before beginning.  if we fail, we cannot start rotating certificates
	if err := c.syncServiceHostnames(); err != nil {
		panic(err)
	}
	if err := c.syncExternalLoadBalancerHostnames(); err != nil {
		panic(err)
	}
	if err := c.syncInternalLoadBalancerHostnames(); err != nil {
		panic(err)
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

func (c *CertRotationController) Run(ctx context.Context, workers int) {
	klog.Infof("Starting CertRotation")
	defer klog.Infof("Shutting down CertRotation")
	c.WaitForReady(ctx.Done())

	go wait.Until(c.runServiceHostnames, time.Second, ctx.Done())
	go wait.Until(c.runExternalLoadBalancerHostnames, time.Second, ctx.Done())
	go wait.Until(c.runInternalLoadBalancerHostnames, time.Second, ctx.Done())

	for _, certRotator := range c.certRotators {
		go certRotator.Run(ctx, workers)
	}

	<-ctx.Done()
}
