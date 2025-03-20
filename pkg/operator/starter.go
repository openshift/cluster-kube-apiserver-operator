package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned"
	operatorv1informers "github.com/openshift/client-go/operator/informers/externalversions"
	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	securityclient "github.com/openshift/client-go/security/clientset/versioned"
	securityvnformers "github.com/openshift/client-go/security/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationtimeupgradeablecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configmetrics"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/apienablement"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/auth"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/node"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/connectivitycheckcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/highcpuusagealertcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/kubeletversionskewcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/nodekubeconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/podsecurityreadinesscontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/sccreconcilecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/serviceaccountissuercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/startupmonitorreadiness"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/terminationobserver"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/webhooksupportabilitycontroller"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/apiserver/controller/auditpolicy"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption"
	"github.com/openshift/library-go/pkg/operator/encryption/controllers/migrators"
	encryptiondeployer "github.com/openshift/library-go/pkg/operator/encryption/deployer"
	"github.com/openshift/library-go/pkg/operator/eventwatch"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/latencyprofilecontroller"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staleconditions"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/common"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/installer"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/revision"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/policy/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	kubemigratorclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	migrationv1alpha1informer "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/informer"
)

func RunOperator(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	// This kube client use protobuf, do not use it for CR
	kubeClient, err := kubernetes.NewForConfig(controllerContext.ProtoKubeConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(controllerContext.ProtoKubeConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	securityClient, err := securityclient.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	operatorcontrolplaneClient, err := operatorcontrolplaneclient.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	apiextensionsClient, err := apiextensionsclient.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	migrationClient, err := kubemigratorclient.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	operandKubernetesVersion, err := semver.Parse(status.VersionForOperandFromEnv())
	if err != nil {
		return err
	}
	groupVersionsByFeatureGate, err := apienablement.GetDefaultGroupVersionByFeatureGate(operandKubernetesVersion)
	if err != nil {
		return err
	}

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
		"kube-system", // system:openshift:controller:kube-apiserver-check-endpoints role binding
		"openshift-etcd",
		"openshift-apiserver",
	)
	configInformers := configv1informers.NewSharedInformerFactory(configClient, 10*time.Minute)
	operatorClient, dynamicInformersForAllNamespaces, err := genericoperatorclient.NewStaticPodOperatorClient(
		controllerContext.Clock,
		controllerContext.KubeConfig,
		operatorv1.GroupVersion.WithResource("kubeapiservers"),
		operatorv1.GroupVersion.WithKind("KubeAPIServer"),
		ExtractStaticPodOperatorSpec,
		ExtractStaticPodOperatorStatus,
	)
	if err != nil {
		return err
	}

	securityInformers := securityvnformers.NewSharedInformerFactory(securityClient, 10*time.Minute)

	desiredVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"

	// By default, this will exit(0) the process if the featuregates ever change to a different set of values.
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(), configInformers.Config().V1().FeatureGates(),
		controllerContext.EventRecorder,
	)
	go featureGateAccessor.Run(ctx)
	go configInformers.Start(ctx.Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: knownFeatureGates=%v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		klog.Errorf("timed out waiting for FeatureGate detection")
		return fmt.Errorf("timed out waiting for FeatureGate detection")
	}

	resourceSyncController, err := resourcesynccontroller.NewResourceSyncController(
		operatorClient,
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
	)
	if err != nil {
		return err
	}

	operatorV1Client, err := operatorv1client.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	operatorInformers := operatorv1informers.NewSharedInformerFactory(operatorV1Client, 10*time.Minute)

	configObserver := configobservercontroller.NewConfigObserver(
		operatorClient,
		kubeInformersForNamespaces,
		configInformers,
		operatorInformers,
		resourceSyncController,
		featureGateAccessor,
		controllerContext.EventRecorder,
		groupVersionsByFeatureGate,
	)

	serviceAccountIssuerController := serviceaccountissuercontroller.NewController(operatorV1Client.OperatorV1().KubeAPIServers(), operatorInformers, configInformers, controllerContext.EventRecorder)

	eventWatcher := eventwatch.New().
		WithEventHandler(operatorclient.TargetNamespace, "LateConnections", terminationobserver.ProcessLateConnectionEvents).
		ToController(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace), kubeClient.CoreV1(), controllerContext.EventRecorder)

	// TODO: use informer instead of direct api call
	// Also, in the future there is a plan to make infrastructure type dynamic
	infrastructure, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}
	var notOnSingleReplicaTopology resourceapply.ConditionalFunction = func() bool {
		return infrastructure.Status.ControlPlaneTopology != configv1.SingleReplicaTopologyMode
	}

	staticResourceController := staticresourcecontroller.NewStaticResourceController(
		"KubeAPIServerStaticResources",
		bindata.Asset,
		[]string{
			"assets/kube-apiserver/ns.yaml",
			"assets/kube-apiserver/svc.yaml",
			"assets/kube-apiserver/kubeconfig-cm.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrole.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrole-node-reader.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrole-crd-reader.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrolebinding-auth-delegator.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrolebinding-node-reader.yaml",
			"assets/kube-apiserver/check-endpoints-clusterrolebinding-crd-reader.yaml",
			"assets/kube-apiserver/check-endpoints-kubeconfig-cm.yaml",
			"assets/kube-apiserver/check-endpoints-rolebinding-kube-system.yaml",
			"assets/kube-apiserver/check-endpoints-rolebinding.yaml",
			"assets/kube-apiserver/control-plane-node-kubeconfig-cm.yaml",
			"assets/kube-apiserver/delegated-incluster-authentication-rolebinding.yaml",
			"assets/kube-apiserver/localhost-recovery-client-crb.yaml",
			"assets/kube-apiserver/localhost-recovery-sa.yaml",
			"assets/kube-apiserver/localhost-recovery-token.yaml",
			"assets/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml",
			"assets/kube-apiserver/storage-version-migration-flowschema.yaml",
			"assets/kube-apiserver/storage-version-migration-prioritylevelconfiguration.yaml",
			"assets/kube-apiserver/storage-version-migration-flowschema-v1beta3.yaml",
			"assets/kube-apiserver/storage-version-migration-prioritylevelconfiguration-v1beta3.yaml",
			"assets/alerts/api-usage.yaml",
			"assets/alerts/audit-errors.yaml",
			"assets/alerts/kube-apiserver-requests.yaml",
			"assets/alerts/kube-apiserver-slos-basic.yaml",
			"assets/alerts/podsecurity-violations.yaml",
		},
		(&resourceapply.ClientHolder{}).
			WithKubernetes(kubeClient).
			WithAPIExtensionsClient(apiextensionsClient).
			WithDynamicClient(dynamicClient).
			WithMigrationClient(migrationClient),
		operatorClient,
		controllerContext.EventRecorder,
	).
		WithConditionalResources(bindata.Asset, []string{"assets/alerts/kube-apiserver-slos-extended.yaml"}, notOnSingleReplicaTopology, nil).
		AddKubeInformers(kubeInformersForNamespaces)

	dynamicInformersForTargetNamespace := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 12*time.Hour, operatorclient.TargetNamespace, nil)

	highCpuUsageAlertController := highcpuusagealertcontroller.NewHighCPUUsageAlertController(
		configInformers.Config().V1(),
		dynamicInformersForTargetNamespace,
		dynamicClient,
		controllerContext.EventRecorder)

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		os.Getenv("IMAGE"),
		os.Getenv("OPERATOR_IMAGE"),
		os.Getenv("OPERATOR_IMAGE_VERSION"),
		operatorClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeInformersForNamespaces,
		kubeClient,
		startupmonitorreadiness.IsStartupMonitorEnabledFunction(configInformers.Config().V1().Infrastructures().Lister(), operatorClient),
		notOnSingleReplicaTopology,
		controllerContext.EventRecorder,
	)

	nodeKubeconfigController := nodekubeconfigcontroller.NewNodeKubeconfigController(
		operatorClient,
		kubeInformersForNamespaces,
		kubeClient,
		configInformers.Config().V1().Infrastructures(),
		controllerContext.EventRecorder,
	)

	apiextensionsInformers := apiextensionsinformers.NewSharedInformerFactory(apiextensionsClient, 10*time.Minute)
	connectivityCheckController := connectivitycheckcontroller.NewKubeAPIServerConnectivityCheckController(
		kubeClient,
		operatorClient,
		apiextensionsClient,
		kubeInformersForNamespaces,
		operatorcontrolplaneClient,
		configInformers,
		apiextensionsInformers,
		controllerContext.EventRecorder,
	)

	// don't change any versions until we sync
	versionRecorder := status.NewVersionGetter()
	clusterOperator, err := configClient.ConfigV1().ClusterOperators().Get(ctx, "kube-apiserver", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	for _, version := range clusterOperator.Status.Versions {
		versionRecorder.SetVersion(version.Name, version.Version)
	}
	versionRecorder.SetVersion("raw-internal", status.VersionForOperatorFromEnv())

	staticPodControllers, err := staticpod.NewBuilder(operatorClient, kubeClient, kubeInformersForNamespaces, configInformers, controllerContext.Clock).
		WithEvents(controllerContext.EventRecorder).
		WithCustomInstaller([]string{"cluster-kube-apiserver-operator", "installer"}, installerErrorInjector(operatorClient)).
		WithPruning([]string{"cluster-kube-apiserver-operator", "prune"}, "kube-apiserver-pod").
		WithRevisionedResources(operatorclient.TargetNamespace, "kube-apiserver", RevisionConfigMaps, RevisionSecrets).
		WithUnrevisionedCerts("kube-apiserver-certs", CertConfigMaps, CertSecrets).
		WithVersioning("kube-apiserver", versionRecorder).
		WithMinReadyDuration(30*time.Second).
		WithStartupMonitor(startupmonitorreadiness.IsStartupMonitorEnabledFunction(configInformers.Config().V1().Infrastructures().Lister(), operatorClient)).
		WithPodDisruptionBudgetGuard(
			"openshift-kube-apiserver-operator",
			"cluster-kube-apiserver-operator",
			"6443",
			"readyz",
			ptr.To(v1.AlwaysAllow),
			func() (bool, bool, error) {
				isSNO, precheckSucceeded, err := common.NewIsSingleNodePlatformFn(configInformers.Config().V1().Infrastructures())()
				// create only when not a single node topology
				return !isSNO, precheckSucceeded, err
			},
		).
		WithOperandPodLabelSelector(labels.Set{"apiserver": "true"}.AsSelector()).
		ToControllers()
	if err != nil {
		return err
	}

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"kube-apiserver",
		[]configv1.ObjectReference{
			{Group: "operator.openshift.io", Resource: "kubeapiservers", Name: "cluster"},
			{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
			{Group: "security.openshift.io", Resource: "securitycontextconstraints"},
			{Resource: "namespaces", Name: operatorclient.GlobalUserSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.GlobalMachineSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.OperatorNamespace},
			{Resource: "namespaces", Name: operatorclient.TargetNamespace},
			{Group: "admissionregistration.k8s.io", Resource: "mutatingwebhookconfigurations"},
			{Group: "admissionregistration.k8s.io", Resource: "validatingwebhookconfigurations"},
			{Group: "controlplane.operator.openshift.io", Resource: "podnetworkconnectivitychecks", Namespace: "openshift-kube-apiserver"},
			{Group: "apiserver.openshift.io", Resource: "apirequestcounts"},
			{Group: "config.openshift.io", Resource: "nodes", Name: "cluster"},
		},

		configClient.ConfigV1(),
		configInformers.Config().V1().ClusterOperators(),
		operatorClient,
		versionRecorder,
		controllerContext.EventRecorder,
		controllerContext.Clock,
	)

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		kubeClient,
		operatorClient,
		configInformers,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder.WithComponentSuffix("cert-rotation-controller"),
		featureGateAccessor,
	)
	if err != nil {
		return err
	}

	staticPodNodeProvider := encryptiondeployer.StaticPodNodeProvider{OperatorClient: operatorClient}
	deployer, err := encryptiondeployer.NewRevisionLabelPodDeployer("revision", operatorclient.TargetNamespace, kubeInformersForNamespaces, kubeClient.CoreV1(), kubeClient.CoreV1(), staticPodNodeProvider)
	if err != nil {
		return err
	}

	migrationInformer := migrationv1alpha1informer.NewSharedInformerFactory(migrationClient, time.Minute*30)
	migrator := migrators.NewKubeStorageVersionMigrator(migrationClient, migrationInformer.Migration().V1alpha1(), kubeClient.Discovery())

	encryptionControllers, err := encryption.NewControllers(
		operatorclient.TargetNamespace,
		nil,
		encryption.StaticEncryptionProvider{
			schema.GroupResource{Group: "", Resource: "secrets"},
			schema.GroupResource{Group: "", Resource: "configmaps"},
		},
		deployer,
		migrator,
		operatorClient,
		configClient.ConfigV1().APIServers(),
		configInformers.Config().V1().APIServers(),
		kubeInformersForNamespaces,
		kubeClient.CoreV1(),
		controllerContext.EventRecorder,
		resourceSyncController,
	)
	if err != nil {
		return err
	}

	certRotationTimeUpgradeableController := certrotationtimeupgradeablecontroller.NewCertRotationTimeUpgradeableController(
		operatorClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps(),
		controllerContext.EventRecorder.WithComponentSuffix("cert-rotation-controller"),
	)

	terminationObserver := terminationobserver.NewTerminationObserver(
		operatorclient.TargetNamespace,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeClient.CoreV1(),
		controllerContext.EventRecorder,
	)

	boundSATokenSignerController := boundsatokensignercontroller.NewBoundSATokenSignerController(
		operatorClient,
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
	)

	auditPolicyController := auditpolicy.NewAuditPolicyController(
		"kube-apiserver",
		operatorclient.TargetNamespace,
		"kube-apiserver-audit-policies",
		operatorClient,
		kubeClient,
		configInformers,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		controllerContext.EventRecorder,
	)

	staleConditionsController := staleconditions.NewRemoveStaleConditionsController(
		"kube-apiserver",
		[]string{
			// the static pod operator used to directly set these. this removes those conditions since the static pod operator was updated.
			// these can be removed in 4.5
			"Available", "Progressing",
			// webhook supportability controller used to set these but are now renamed
			"MutatingAdmissionWebhookConfigurationDegraded",
			"ValidatingAdmissionWebhookConfigurationDegraded",
			"CRDConversionWebhookConfigurationDegraded",
			"VirtualResourceAdmissionDegraded",
		},
		operatorClient,
		controllerContext.EventRecorder,
	)

	kubeletVersionSkewController := kubeletversionskewcontroller.NewKubeletVersionSkewController(
		operatorClient,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
	)

	latencyProfileController := latencyprofilecontroller.NewLatencyProfileController(
		"kube-apiserver",
		operatorClient,
		operatorclient.TargetNamespace,
		nil, // profile rejection logic is not required for this operator
		latencyprofilecontroller.NewInstallerRevisionConfigMatcher(
			kubeInformersForNamespaces.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace),
			node.LatencyConfigs,
		),
		configInformers.Config().V1().Nodes(),
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
	)

	webhookSupportabilityController := webhooksupportabilitycontroller.NewWebhookSupportabilityController(
		operatorClient,
		kubeInformersForNamespaces,
		apiextensionsInformers,
		controllerContext.EventRecorder,
	)

	sccReconcileController, err := sccreconcilecontroller.NewSCCReconcileController(
		securityClient.SecurityV1(),
		securityInformers.Security().V1().SecurityContextConstraints(),
		controllerContext.EventRecorder,
	)

	podSecurityReadinessController, err := podsecurityreadinesscontroller.NewPodSecurityReadinessController(
		controllerContext.ProtoKubeConfig,
		operatorClient,
		controllerContext.EventRecorder,
	)
	if err != nil {
		return err
	}

	// register termination metrics
	terminationobserver.RegisterMetrics()

	// register config metrics
	configmetrics.Register(configInformers)

	kubeInformersForNamespaces.Start(ctx.Done())
	configInformers.Start(ctx.Done())
	dynamicInformersForAllNamespaces.Start(ctx.Done())
	dynamicInformersForTargetNamespace.Start(ctx.Done())
	migrationInformer.Start(ctx.Done())
	apiextensionsInformers.Start(ctx.Done())
	operatorInformers.Start(ctx.Done())
	securityInformers.Start(ctx.Done())

	go staticPodControllers.Start(ctx)
	go resourceSyncController.Run(ctx, 1)
	go staticResourceController.Run(ctx, 1)
	go targetConfigReconciler.Run(ctx, 1)
	go nodeKubeconfigController.Run(ctx, 1)
	go configObserver.Run(ctx, 1)
	go clusterOperatorStatus.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)
	go encryptionControllers.Run(ctx, 1)
	go certRotationTimeUpgradeableController.Run(ctx, 1)
	go terminationObserver.Run(ctx, 1)
	go eventWatcher.Run(ctx, 1)
	go boundSATokenSignerController.Run(ctx, 1)
	go auditPolicyController.Run(ctx, 1)
	go staleConditionsController.Run(ctx, 1)
	go connectivityCheckController.Run(ctx, 1)
	go kubeletVersionSkewController.Run(ctx, 1)
	go latencyProfileController.Run(ctx, 1)
	go webhookSupportabilityController.Run(ctx, 1)
	go serviceAccountIssuerController.Run(ctx, 1)
	go podSecurityReadinessController.Run(ctx, 1)
	go highCpuUsageAlertController.Run(ctx, 1)
	go sccReconcileController.Run(ctx, 1)

	<-ctx.Done()
	return nil
}

// installerErrorInjector mutates the given installer pod to fail or OOM depending on the propability (
// - 0 <= unsupportedConfigOverrides.installerErrorInjection.failPropability <= 1.0: fail the pod (crash loop)
// - 0 <= unsupportedConfigOverrides.installerErrorInjection.oomPropability <= 1.0: cause OOM due to 1 MB memory limits
func installerErrorInjector(operatorClient v1helpers.StaticPodOperatorClient) func(pod *corev1.Pod, nodeName string, operatorSpec *operatorv1.StaticPodOperatorSpec, revision int32) error {
	return func(pod *corev1.Pod, nodeName string, operatorSpec *operatorv1.StaticPodOperatorSpec, revision int32) error {
		// get UnsupportedConfigOverrides
		spec, _, _, err := operatorClient.GetOperatorState()
		if err != nil {
			klog.Warningf("failed to get operator/v1 spec for error injection: %v", err)
			return nil // ignore error
		}
		if len(spec.UnsupportedConfigOverrides.Raw) == 0 {
			return nil
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(spec.UnsupportedConfigOverrides.Raw, &obj); err != nil {
			klog.Warningf("failed to unmarshal operator/v1 spec.unsupportedConfigOverrides for error injection: %v", err)
			return nil
		}

		if failPropability, found, err := nestedFloat64OrInt(obj, "installerErrorInjection", "failPropability"); err == nil && found {
			if rand.Float64() < failPropability {
				pod.Spec.Containers[0].Command = []string{"false"}
			}
		}

		if oomPropability, found, err := nestedFloat64OrInt(obj, "installerErrorInjection", "oomPropability"); err == nil && found {
			if rand.Float64() < oomPropability {
				twoMB := resource.NewQuantity(int64(2000000), resource.DecimalSI) // instead of 200M
				for n := range pod.Spec.Containers[0].Resources.Limits {
					if n == corev1.ResourceMemory {
						pod.Spec.Containers[0].Resources.Limits[n] = *twoMB
					}
				}
				for n := range pod.Spec.Containers[0].Resources.Requests {
					if n == corev1.ResourceMemory {
						pod.Spec.Containers[0].Resources.Requests[n] = *twoMB
					}
				}
			}
		}

		return nil
	}
}

func nestedFloat64OrInt(obj map[string]interface{}, fields ...string) (float64, bool, error) {
	if x, found, err := unstructured.NestedFloat64(obj, fields...); err == nil && !found {
		return 0.0, false, nil
	} else if err == nil && found {
		return x, found, err
	}
	x, found, err := unstructured.NestedInt64(obj, fields...)
	return float64(x), found, err
}

// RevisionConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var RevisionConfigMaps = []revision.RevisionResource{
	{Name: "kube-apiserver-pod"},

	{Name: "config"},
	{Name: "kube-apiserver-cert-syncer-kubeconfig"},
	{Name: "oauth-metadata", Optional: true},
	{Name: "cloud-config", Optional: true},

	// This configmap is managed by the operator, but ensuring a revision history
	// supports signing key promotion. Promotion requires knowing whether the current
	// public key is present in the configmap(s) associated with the current
	// revision(s) of the master nodes.
	{Name: "bound-sa-token-signing-certs"},

	// these need to removed, but if we remove them now, the cluster will die because we don't reload them yet
	{Name: "etcd-serving-ca"},
	{Name: "kube-apiserver-server-ca", Optional: true},
	{Name: "kubelet-serving-ca"},
	{Name: "sa-token-signing-certs"},

	{Name: "kube-apiserver-audit-policies"},

	// optional configmap containing the OIDC structured auth config
	{Name: auth.AuthConfigCMName, Optional: true},
}

// RevisionSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var RevisionSecrets = []revision.RevisionResource{
	// these need to removed, but if we remove them now, the cluster will die because we don't reload them yet
	{Name: "etcd-client"},
	// etcd encryption
	{Name: "encryption-config", Optional: true},

	// this needs to be revisioned as certsyncer's kubeconfig isn't wired to be live reloaded, nor will be autorecovery
	{Name: "localhost-recovery-serving-certkey"},
	{Name: "localhost-recovery-client-token"},

	{Name: "webhook-authenticator", Optional: true},
}

var CertConfigMaps = []installer.UnrevisionedResource{
	{Name: "aggregator-client-ca"},
	{Name: "client-ca"},

	// this is a copy of trusted-ca-bundle CM without the injection annotations
	{Name: "trusted-ca-bundle", Optional: true},

	// kubeconfig that is a system:master.  this ensures a stable location
	{Name: "control-plane-node-kubeconfig"},

	// kubeconfig for check-endpoints
	{Name: "check-endpoints-kubeconfig"},
}

var CertSecrets = []installer.UnrevisionedResource{
	{Name: "aggregator-client"},
	{Name: "localhost-serving-cert-certkey"},
	{Name: "service-network-serving-certkey"},
	{Name: "external-loadbalancer-serving-certkey"},
	{Name: "internal-loadbalancer-serving-certkey"},
	{Name: "bound-service-account-signing-key"},
	{Name: "control-plane-node-admin-client-cert-key"},
	{Name: "check-endpoints-client-cert-key"},
	{Name: "kubelet-client"},

	{Name: "node-kubeconfigs"},

	{Name: "user-serving-cert", Optional: true},
	{Name: "user-serving-cert-000", Optional: true},
	{Name: "user-serving-cert-001", Optional: true},
	{Name: "user-serving-cert-002", Optional: true},
	{Name: "user-serving-cert-003", Optional: true},
	{Name: "user-serving-cert-004", Optional: true},
	{Name: "user-serving-cert-005", Optional: true},
	{Name: "user-serving-cert-006", Optional: true},
	{Name: "user-serving-cert-007", Optional: true},
	{Name: "user-serving-cert-008", Optional: true},
	{Name: "user-serving-cert-009", Optional: true},
}

func ExtractStaticPodOperatorSpec(obj *unstructured.Unstructured, fieldManager string) (*applyoperatorv1.StaticPodOperatorSpecApplyConfiguration, error) {
	castObj := &operatorv1.KubeAPIServer{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, castObj); err != nil {
		return nil, fmt.Errorf("unable to convert to KubeControllerManager: %w", err)
	}
	ret, err := applyoperatorv1.ExtractKubeAPIServer(castObj, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("unable to extract fields for %q: %w", fieldManager, err)
	}
	if ret.Spec == nil {
		return nil, nil
	}
	return &ret.Spec.StaticPodOperatorSpecApplyConfiguration, nil
}

func ExtractStaticPodOperatorStatus(obj *unstructured.Unstructured, fieldManager string) (*applyoperatorv1.StaticPodOperatorStatusApplyConfiguration, error) {
	castObj := &operatorv1.KubeAPIServer{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, castObj); err != nil {
		return nil, fmt.Errorf("unable to convert to KubeAPIServer: %w", err)
	}
	ret, err := applyoperatorv1.ExtractKubeAPIServerStatus(castObj, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("unable to extract fields for %q: %w", fieldManager, err)
	}

	if ret.Status == nil {
		return nil, nil
	}
	return &ret.Status.StaticPodOperatorStatusApplyConfiguration, nil
}
