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

	aggregatorClientSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "aggregator-client-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions openshift-apiserver'",
		},
		Validity:      30 * rotationDay,
		Refresh:       15 * rotationDay,
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	aggregatorClientSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&aggregatorClientSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, aggregatorClientSignerRotator)

	aggregatorClientCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		Name:      "kube-apiserver-aggregator-client-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions openshift-apiserver'",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	aggregatorClientCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&aggregatorClientCABundle,
		[]*certrotation.RotatedSigningCASecret{&aggregatorClientSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, aggregatorClientCABundleRotator)

	aggregatorClientTargetCert := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "aggregator-client",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions openshift-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:openshift-aggregator"},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	aggregatorClientTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		aggregatorClientTargetCert,
		&aggregatorClientSigner,
		&aggregatorClientCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, aggregatorClientTargetCertRotator)

	kubeApiserverToKubeletSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "kube-apiserver-to-kubelet-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
		},
		Validity: 1 * 365 * defaultRotationDay, // this comes from the installer
		// Refresh set to 80% of the validity.
		// This range is consistent with most other signers defined in this pkg.
		Refresh:       292 * defaultRotationDay,
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	kubeApiserverToKubeletSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&kubeApiserverToKubeletSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeApiserverToKubeletSignerRotator)

	kubeApiserverToKubeletCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "kube-apiserver-to-kubelet-client-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	kubeApiserverToKubeletCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&kubeApiserverToKubeletCABundle,
		[]*certrotation.RotatedSigningCASecret{&kubeApiserverToKubeletSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeApiserverToKubeletCABundleRotator)

	kubeApiserverToKubeletTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "kubelet-client",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:kube-apiserver", Groups: []string{"kube-master"}},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	kubeApiserverToKubeletTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		kubeApiserverToKubeletTarget,
		&kubeApiserverToKubeletSigner,
		&kubeApiserverToKubeletCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeApiserverToKubeletTargetCertRotator)

	localhostServingSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-serving-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	localhostServingSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&localhostServingSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostServingSignerRotator)

	localhostServingCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-serving-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	localhostServingCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&localhostServingCABundle,
		[]*certrotation.RotatedSigningCASecret{&localhostServingSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostServingCABundleRotator)

	localhostServingTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "localhost-serving-cert-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ServingRotation{
			Hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	localhostServingTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		localhostServingTarget,
		&localhostServingSigner,
		&localhostServingCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostServingTargetCertRotator)

	serviceNetworkSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "service-network-serving-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	serviceNetworkSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&serviceNetworkSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, serviceNetworkSignerRotator)

	serviceNetworkCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "service-network-serving-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	serviceNetworkCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&serviceNetworkCABundle,
		[]*certrotation.RotatedSigningCASecret{&serviceNetworkSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, serviceNetworkCABundleRotator)

	serviceNetworkTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "service-network-serving-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		Informer: kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:   kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		CertCreator: &certrotation.ServingRotation{
			Hostnames:        ret.serviceNetwork.GetHostnames,
			HostnamesChanged: ret.serviceNetwork.hostnamesChanged,
		},
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	serviceNetworkTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		serviceNetworkTarget,
		&serviceNetworkSigner,
		&serviceNetworkCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, serviceNetworkTargetCertRotator)

	loadbalancerServingSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "loadbalancer-serving-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	loadbalancerServingSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&loadbalancerServingSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, loadbalancerServingSignerRotator)

	loadbalancerServingCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "loadbalancer-serving-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	loadbalancerServingCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&loadbalancerServingCABundle,
		[]*certrotation.RotatedSigningCASecret{&loadbalancerServingSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, loadbalancerServingCABundleRotator)

	externalLoadbalancerServingTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "external-loadbalancer-serving-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ServingRotation{
			Hostnames:        ret.externalLoadBalancer.GetHostnames,
			HostnamesChanged: ret.externalLoadBalancer.hostnamesChanged,
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	externalLoadbalancerServingTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		externalLoadbalancerServingTarget,
		&loadbalancerServingSigner,
		&loadbalancerServingCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, externalLoadbalancerServingTargetCertRotator)

	internalLoadbalancerServingTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "internal-loadbalancer-serving-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ServingRotation{
			Hostnames:        ret.internalLoadBalancer.GetHostnames,
			HostnamesChanged: ret.internalLoadBalancer.hostnamesChanged,
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	internalLoadbalancerServingTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		internalLoadbalancerServingTarget,
		&loadbalancerServingSigner,
		&loadbalancerServingCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, internalLoadbalancerServingTargetCertRotator)

	localhostRecoveryServingSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-recovery-serving-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	localhostRecoveryServingSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&localhostRecoveryServingSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostRecoveryServingSignerRotator)

	localhostRecoveryServingCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "localhost-recovery-serving-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	localhostRecoveryServingCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&localhostRecoveryServingCABundle,
		[]*certrotation.RotatedSigningCASecret{&localhostRecoveryServingSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostRecoveryServingCABundleRotator)

	localhostRecoveryServingTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "localhost-recovery-serving-certkey",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	localhostRecoveryServingTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		localhostRecoveryServingTarget,
		&localhostRecoveryServingSigner,
		&localhostRecoveryServingCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, localhostRecoveryServingTargetCertRotator)

	kubeControlPlaneSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "kube-control-plane-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-controller-manager'",
		},
		Validity:      60 * defaultRotationDay,
		Refresh:       30 * defaultRotationDay,
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	kubeControlPlaneSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&kubeControlPlaneSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeControlPlaneSignerRotator)

	kubeControlPlaneCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "kube-control-plane-signer-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-controller-manager'",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	kubeControlPlaneCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&kubeControlPlaneCABundle,
		[]*certrotation.RotatedSigningCASecret{&kubeControlPlaneSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeControlPlaneCABundleRotator)

	kubeControllerTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		Name:      "kube-controller-manager-client-cert-key",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-controller-manager'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:kube-controller-manager"},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	kubeControllerTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		kubeControllerTarget,
		&kubeControlPlaneSigner,
		&kubeControlPlaneCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeControllerTargetCertRotator)

	kubeSchedulerTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		Name:      "kube-scheduler-client-cert-key",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-controller-manager'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:kube-scheduler"},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	kubeSchedulerTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		kubeSchedulerTarget,
		&kubeControlPlaneSigner,
		&kubeControlPlaneCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, kubeSchedulerTargetCertRotator)

	nodeAdminTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "control-plane-node-admin-client-cert-key",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:control-plane-node-admin", Groups: []string{"system:masters"}},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	nodeAdminTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		nodeAdminTarget,
		&kubeControlPlaneSigner,
		&kubeControlPlaneCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, nodeAdminTargetCertRotator)

	checkEndpointsTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.TargetNamespace,
		Name:      "check-endpoints-client-cert-key",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 30 * rotationDay,
		Refresh:  15 * rotationDay,
		CertCreator: &certrotation.ClientRotation{
			UserInfo: &user.DefaultInfo{Name: "system:serviceaccount:openshift-kube-apiserver:check-endpoints"},
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	checkEndpointsTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		checkEndpointsTarget,
		&kubeControlPlaneSigner,
		&kubeControlPlaneCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, checkEndpointsTargetCertRotator)

	nodeSystemAdminSigner := certrotation.RotatedSigningCASecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "node-system-admin-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Validity: 1 * 365 * defaultRotationDay,
		// Refresh set to 80% of the validity.
		// This range is consistent with most other signers defined in this pkg.
		Refresh:       292 * defaultRotationDay,
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	nodeSystemAdminSignerRotator := certrotation.NewRotatedSigningCASecretController(
		&nodeSystemAdminSigner,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, nodeSystemAdminSignerRotator)

	nodeSystemAdminCABundle := certrotation.CABundleConfigMap{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "node-system-admin-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
		Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
		Client:        kubeClient.CoreV1(),
		EventRecorder: eventRecorder,
	}
	nodeSystemAdminCABundleRotator := certrotation.NewRotatedCABundleConfigMapController(
		&nodeSystemAdminCABundle,
		[]*certrotation.RotatedSigningCASecret{&nodeSystemAdminSigner},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, nodeSystemAdminCABundleRotator)

	nodeSystemAdminTarget := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: operatorclient.OperatorNamespace,
		Name:      "node-system-admin-client",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent:                    "kube-apiserver",
			AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631,'operator conditions kube-apiserver'",
		},
		// This needs to live longer then control plane certs so there is high chance that if a cluster breaks
		// because of expired certs these are still valid to use for collecting data using localhost-recovery
		// endpoint with long lived serving certs for localhost.
		Validity: 2 * 365 * defaultRotationDay,
		// We rotate sooner so certs are always valid for 90 days (30 days more then kube-control-plane-signer)
		Refresh: 30 * defaultRotationDay,
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

		// we will remove this when we migrate all of the affected secret
		// objects to their intended type: https://issues.redhat.com/browse/API-1800
		UseSecretUpdateOnly: true,
	}
	nodeSystemAdminTargetCertRotator := certrotation.NewRotatedTargetSecretController(
		nodeSystemAdminTarget,
		&nodeSystemAdminSigner,
		&nodeSystemAdminCABundle,
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)
	ret.certRotators = append(ret.certRotators, nodeSystemAdminTargetCertRotator)
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
