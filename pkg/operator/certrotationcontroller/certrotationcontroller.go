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
	featureGateAccessor featuregates.FeatureGateAccess,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGateAccessor,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGateAccessor featuregates.FeatureGateAccess,
) (*CertRotationController, error) {
	return newCertRotationController(
		kubeClient,
		operatorClient,
		configInformer,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGateAccessor,
		true,
	)
}

func newCertRotationController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	configInformer configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGateAccessor featuregates.FeatureGateAccess,
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

	// Some certificates should not be affected by development cycle rotation
	devRotationExceptionDay := 24 * time.Hour

	monthPeriod := 30 * rotationDay
	devRotationExceptionMonth := 30 * devRotationExceptionDay
	yearPeriod := 365 * rotationDay
	devRotationExceptionYear := 365 * devRotationExceptionDay
	tenMonthPeriod := 292 * rotationDay
	devRotationExceptionTenMonth := 292 * devRotationExceptionDay

	// Set custom rotation duration when FeatureShortCertRotation is enabled
	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return nil, fmt.Errorf("unable to get FeatureGates: %w", err)
	}

	if featureGates.Enabled(features.FeatureShortCertRotation) {
		monthPeriod = 2 * time.Hour
		devRotationExceptionMonth = monthPeriod
		yearPeriod = 3 * time.Hour
		devRotationExceptionYear = yearPeriod
		tenMonthPeriod = 150 * time.Minute
		devRotationExceptionTenMonth = tenMonthPeriod
	}
	klog.Infof("Setting monthPeriod to %v, yearPeriod to %v, tenMonthPeriod to %v", monthPeriod, yearPeriod, tenMonthPeriod)

	certRotator := certrotation.NewCertRotationController(
		"AggregatorProxyClientCert",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "aggregator-client-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the kube-apiserver to create client certificates for aggregated apiservers to recognize as a front-proxy",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for aggregated apiservers to recognize kube-apiserver as front-proxy.",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "aggregator-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-apiserver to communicate to aggregated apiservers.",
				TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the kube-apiserver-to-kubelet-client so kubelets can recognize the kube-apiserver.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: devRotationExceptionYear, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			Refresh:                devRotationExceptionMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for the kubelet to recognize the kube-apiserver client certificate.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "kubelet-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-apiserver to authenticate to the kubelet for requests like exec and logs.",
				TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via localhost.",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: foreverPeriod, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                foreverRefreshPeriod,
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
				Description:   "CA for recognizing the kube-apiserver when connecting via localhost.",
				// LocalhostServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "localhost-serving-cert-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via localhost.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: foreverPeriod, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                foreverRefreshPeriod,
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
				Description:   "CA for recognizing the kube-apiserver when connecting via the service network (kubernetes.default.svc).",
				// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "service-network-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the service network.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: foreverPeriod, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                foreverRefreshPeriod,
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
				Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
				// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "external-loadbalancer-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the external load balancer.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: foreverPeriod, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:                foreverRefreshPeriod,
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
				Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
				// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "internal-loadbalancer-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the internal load balancer.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Validity:               foreverPeriod, // this comes from the installer
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh:       foreverRefreshPeriod,
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
				Description:   "CA for recognizing the kube-apiserver when connecting via the localhost recovery SNI ServerName.",
				// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
				// other signers are updated and needs to have the same metadata set
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "localhost-recovery-serving-certkey",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-apiserver",
				Description:   "Serving certificate used by the kube-apiserver to terminate requests via the localhost recovery SNI ServerName.",
				// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: foreverPeriod,
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			// Given that in this case rotation will be after 8y,
			// it means we effectively do not rotate.
			Refresh: foreverRefreshPeriod,
			CertCreator: &certrotation.ServingRotation{
				Hostnames: func() []string { return []string{"localhost-recovery"} },
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               2 * devRotationExceptionMonth,
			Refresh:                devRotationExceptionMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:      "kube-controller-manager-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-controller-manager to authenticate to the kube-apiserver.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               2 * devRotationExceptionMonth,
			Refresh:                devRotationExceptionMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			Name:      "kube-scheduler-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the kube-scheduler to authenticate to the kube-apiserver.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               2 * devRotationExceptionMonth,
			Refresh:                devRotationExceptionMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "control-plane-node-admin-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate and key for the control plane node kubeconfig",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"control-plane-node.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               2 * devRotationExceptionMonth,
			Refresh:                devRotationExceptionMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
				TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.TargetNamespace,
			Name:      "check-endpoints-client-cert-key",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate used by the network connectivity checker of the kube-apiserver.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"check-endpoints.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity:               monthPeriod,
			Refresh:                monthPeriod / 2,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "Signer for the per-master-debugging-client.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			Validity: 3 * devRotationExceptionYear,
			// Refresh set to 80% of the validity.
			// This range is consistent with most other signers defined in this pkg.
			Refresh:                3 * devRotationExceptionTenMonth,
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
				JiraComponent:                    "kube-apiserver",
				Description:                      "CA for kube-apiserver to recognize local system:masters rendered to each master.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:                 kubeClient.CoreV1(),
			EventRecorder:          eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "node-system-admin-client",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent:                    "kube-apiserver",
				Description:                      "Client certificate (system:masters) placed on each master to allow communication to kube-apiserver for debugging.",
				TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
				AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
			},
			// This needs to live longer then control plane certs so there is high chance that if a cluster breaks
			// because of expired certs these are still valid to use for collecting data using localhost-recovery
			// endpoint with long lived serving certs for localhost.
			Validity: 2 * devRotationExceptionYear,
			// We rotate sooner so certs are always valid for 90 days (30 days more then kube-control-plane-signer)
			Refresh:                monthPeriod,
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
