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

	features "github.com/openshift/api/features"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisterv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/openshift/library-go/pkg/pki"
)

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
	featureGates featuregates.FeatureGate,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGates,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGates featuregates.FeatureGate,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGates,
		true,
	)
}

func newCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGates featuregates.FeatureGate,
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

	foreverPeriod := 10 * 365 * 24 * time.Hour
	foreverRefreshPeriod := 8 * 365 * 24 * time.Hour

	rotationDay := 24 * time.Hour
	// for the development cycle, make the rotation 60 times faster (every twelve hours or so).
	// This must be reverted before we ship
	rotationDay = rotationDay / 60

	// Some certificates should not be affected by development cycle rotation
	devRotationExceptionDay := 24 * time.Hour

	monthPeriod := 30 * rotationDay
	devRotationExceptionMonth := 30 * devRotationExceptionDay
	yearPeriod := 365 * rotationDay
	devRotationExceptionYear := 365 * devRotationExceptionDay
	tenMonthPeriod := 292 * rotationDay
	devRotationExceptionTenMonth := 292 * devRotationExceptionDay

	// Set custom rotation duration when FeatureShortCertRotation is enabled
	if featureGates.Enabled(features.FeatureShortCertRotation) {
		monthPeriod = 2 * time.Hour
		devRotationExceptionMonth = monthPeriod
		yearPeriod = 3 * time.Hour
		devRotationExceptionYear = yearPeriod
		tenMonthPeriod = 150 * time.Minute
		devRotationExceptionTenMonth = tenMonthPeriod
	}
	klog.Infof("Setting monthPeriod to %v, yearPeriod to %v, tenMonthPeriod to %v", monthPeriod, yearPeriod, tenMonthPeriod)

	var pkiProvider pki.PKIProfileProvider
	if featureGates != nil && featureGates.Enabled(features.FeatureGateConfigurablePKI) {
		ret.cachesToSync = append(ret.cachesToSync, configInformer.Config().V1alpha1().PKIs().Informer().HasSynced)
		pkiProvider = pki.NewClusterPKIProfileProvider(configInformer.Config().V1alpha1().PKIs().Lister())
	}

	certRotator := certrotation.NewCertRotationController(
		"AggregatorProxyClientCert",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"aggregator-client-signer",
			"kube-apiserver.aggregator-front-proxy-signer",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the kube-apiserver to create client certificates for aggregated apiservers to recognize as a front-proxy",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.GlobalMachineSpecifiedConfigNamespace,
			"kube-apiserver-aggregator-client-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for aggregated apiservers to recognize kube-apiserver as front-proxy.",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"aggregator-client",
			"kube-apiserver.aggregator-front-proxy-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-apiserver to communicate to aggregated apiservers.",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:openshift-aggregator"}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeAPIServerToKubeletClientCert",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"kube-apiserver-to-kubelet-signer",
			"kube-apiserver.kubelet-client-signer",
			devRotationExceptionYear,
			devRotationExceptionTenMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the kube-apiserver-to-kubelet-client so kubelets can recognize the kube-apiserver.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"kube-apiserver-to-kubelet-client-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for the kubelet to recognize the kube-apiserver client certificate.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"kubelet-client",
			"kube-apiserver.kubelet-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-apiserver to authenticate to the kubelet for requests like exec and logs.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:kube-apiserver", Groups: []string{"kube-master"}}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"LocalhostServing",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"localhost-serving-signer",
			"kube-apiserver.localhost-serving-signer",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via localhost.",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"localhost-serving-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "CA for recognizing the kube-apiserver when connecting via localhost.",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"localhost-serving-cert-certkey",
			"kube-apiserver.localhost-serving",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via localhost.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewServingRotation(func() []string { return []string{"localhost", "127.0.0.1"} }, nil, nil),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ServiceNetworkServing",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"service-network-serving-signer",
			"kube-apiserver.service-network-serving-signer",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"service-network-serving-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "CA for recognizing the kube-apiserver when connecting via the service network (kubernetes.default.svc).",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"service-network-serving-certkey",
			"kube-apiserver.service-network-serving",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the service network.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewServingRotation(ret.serviceNetwork.GetHostnames, nil, ret.serviceNetwork.hostnamesChanged),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ExternalLoadBalancerServing",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"loadbalancer-serving-signer",
			"kube-apiserver.loadbalancer-serving-signer",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"loadbalancer-serving-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"external-loadbalancer-serving-certkey",
			"kube-apiserver.external-loadbalancer-serving",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the external load balancer.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewServingRotation(ret.externalLoadBalancer.GetHostnames, nil, ret.externalLoadBalancer.hostnamesChanged),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"InternalLoadBalancerServing",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"loadbalancer-serving-signer",
			"kube-apiserver.loadbalancer-serving-signer",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"loadbalancer-serving-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"internal-loadbalancer-serving-certkey",
			"kube-apiserver.internal-loadbalancer-serving",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the internal load balancer.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewServingRotation(ret.internalLoadBalancer.GetHostnames, nil, ret.internalLoadBalancer.hostnamesChanged),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"LocalhostRecoveryServing",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"localhost-recovery-serving-signer",
			"kube-apiserver.localhost-recovery-serving-signer",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"localhost-recovery-serving-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "CA for recognizing the kube-apiserver when connecting via the localhost recovery SNI ServerName.",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"localhost-recovery-serving-certkey",
			"kube-apiserver.localhost-recovery-serving",
			foreverPeriod,
			foreverRefreshPeriod,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Serving certificate used by the kube-apiserver to terminate requests via the localhost recovery SNI ServerName.",
				// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewServingRotation(func() []string { return []string{"localhost-recovery"} }, nil, nil),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeControllerManagerClient",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer",
			"kube-apiserver.control-plane-client-signer",
			2*devRotationExceptionMonth,
			devRotationExceptionMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.GlobalMachineSpecifiedConfigNamespace,
			"kube-controller-manager-client-cert-key",
			"kube-apiserver.kube-controller-manager-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-controller-manager to authenticate to the kube-apiserver.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:kube-controller-manager"}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeSchedulerClient",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer",
			"kube-apiserver.control-plane-client-signer",
			2*devRotationExceptionMonth,
			devRotationExceptionMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.GlobalMachineSpecifiedConfigNamespace,
			"kube-scheduler-client-cert-key",
			"kube-apiserver.kube-scheduler-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-scheduler to authenticate to the kube-apiserver.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:kube-scheduler"}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ControlPlaneNodeAdminClient",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer",
			"kube-apiserver.control-plane-client-signer",
			2*devRotationExceptionMonth,
			devRotationExceptionMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"control-plane-node-admin-client-cert-key",
			"kube-apiserver.control-plane-node-admin-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate and key for the control plane node kubeconfig",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"control-plane-node.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:control-plane-node-admin", Groups: []string{"system:masters"}}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"CheckEndpointsClient",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer",
			"kube-apiserver.control-plane-client-signer",
			2*devRotationExceptionMonth,
			devRotationExceptionMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"kube-control-plane-signer-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.TargetNamespace,
			"check-endpoints-client-cert-key",
			"kube-apiserver.check-endpoints-client",
			monthPeriod,
			monthPeriod/2,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the network connectivity checker of the kube-apiserver.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"check-endpoints.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{Name: "system:serviceaccount:openshift-kube-apiserver:check-endpoints"}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"NodeSystemAdminClient",
		certrotation.NewRotatedSigningCASecret(
			operatorclient.OperatorNamespace,
			"node-system-admin-signer",
			"kube-apiserver.node-system-admin-signer",
			3*devRotationExceptionYear,
			3*devRotationExceptionTenMonth,
			refreshOnlyWhenExpired,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the per-master-debugging-client.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			nil,
			eventRecorder,
			pkiProvider,
		),
		certrotation.NewCABundleConfigMap(
			operatorclient.OperatorNamespace,
			"node-system-admin-ca",
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize local system:masters rendered to each master.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
		certrotation.NewRotatedSelfSignedCertKeySecret(
			operatorclient.OperatorNamespace,
			"node-system-admin-client",
			"kube-apiserver.node-system-admin-client",
			2*devRotationExceptionYear,
			monthPeriod,
			refreshOnlyWhenExpired,
			nil,
			certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate (system:masters) placed on each master to allow communication to kube-apiserver for debugging.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			certrotation.NewClientRotation(&user.DefaultInfo{
				Name:   "system:admin",
				Groups: []string{"system:masters"},
			}),
			pkiProvider,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			kubeClient.CoreV1(),
			eventRecorder,
		),
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
