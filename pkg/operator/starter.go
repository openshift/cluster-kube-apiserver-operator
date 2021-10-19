package operator

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/boundsatokensignercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationtimeupgradeablecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configmetrics"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/connectivitycheckcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/featureupgradablecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/kubeletversionskewcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/nodekubeconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/startupmonitorreadiness"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/terminationobserver"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/webhooksupportabilitycontroller"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/apiserver/controller/auditpolicy"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/encryption"
	"github.com/openshift/library-go/pkg/operator/encryption/controllers/migrators"
	encryptiondeployer "github.com/openshift/library-go/pkg/operator/encryption/deployer"
	"github.com/openshift/library-go/pkg/operator/eventwatch"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staleconditions"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/installer"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/revision"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
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
	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(controllerContext.KubeConfig, operatorv1.GroupVersion.WithResource("kubeapiservers"))
	if err != nil {
		return err
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

	configObserver := configobservercontroller.NewConfigObserver(
		operatorClient,
		kubeInformersForNamespaces,
		configInformers,
		resourceSyncController,
		controllerContext.EventRecorder,
	)

	eventWatcher := eventwatch.New().
		WithEventHandler(operatorclient.TargetNamespace, "LateConnections", terminationobserver.ProcessLateConnectionEvents).
		ToController(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace), kubeClient.CoreV1(), controllerContext.EventRecorder)

	staticResourceFiles := []string{
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
		"assets/alerts/api-usage.yaml",
		"assets/alerts/audit-errors.yaml",
		"assets/alerts/cpu-utilization.yaml",
		"assets/alerts/kube-apiserver-requests.yaml",
		"assets/alerts/kube-apiserver-slos-basic.yaml",
	}
	infrastructure, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if infrastructure.Status.ControlPlaneTopology != configv1.SingleReplicaTopologyMode {
		staticResourceFiles = append(staticResourceFiles, "assets/alerts/kube-apiserver-slos-extended.yaml")
	}
	staticResourceController := staticresourcecontroller.NewStaticResourceController(
		"KubeAPIServerStaticResources",
		bindata.Asset,
		staticResourceFiles,
		(&resourceapply.ClientHolder{}).
			WithKubernetes(kubeClient).
			WithAPIExtensionsClient(apiextensionsClient).
			WithDynamicClient(dynamicClient).
			WithMigrationClient(migrationClient),
		operatorClient,
		controllerContext.EventRecorder,
	).AddKubeInformers(kubeInformersForNamespaces)

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		os.Getenv("IMAGE"),
		os.Getenv("OPERATOR_IMAGE"),
		operatorClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeInformersForNamespaces,
		kubeClient,
		startupmonitorreadiness.IsStartupMonitorEnabledFunction(configInformers.Config().V1().Infrastructures().Lister(), operatorClient),
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

	staticPodControllers, err := staticpod.NewBuilder(operatorClient, kubeClient, kubeInformersForNamespaces).
		WithEvents(controllerContext.EventRecorder).
		WithCustomInstaller([]string{"cluster-kube-apiserver-operator", "installer"}, installerErrorInjector(operatorClient)).
		WithPruning([]string{"cluster-kube-apiserver-operator", "prune"}, "kube-apiserver-pod").
		WithRevisionedResources(operatorclient.TargetNamespace, "kube-apiserver", RevisionConfigMaps, RevisionSecrets).
		WithUnrevisionedCerts("kube-apiserver-certs", CertConfigMaps, CertSecrets).
		WithVersioning("kube-apiserver", versionRecorder).
		WithMinReadyDuration(30*time.Second).
		WithStartupMonitor(startupmonitorreadiness.IsStartupMonitorEnabledFunction(configInformers.Config().V1().Infrastructures().Lister(), operatorClient), labels.Set{"apiserver": "true"}.AsSelector()).
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
		},

		configClient.ConfigV1(),
		configInformers.Config().V1().ClusterOperators(),
		operatorClient,
		versionRecorder,
		controllerContext.EventRecorder,
	)

	certRotationScale, err := certrotation.GetCertRotationScale(ctx, kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
	if err != nil {
		return err
	}

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		kubeClient,
		operatorClient,
		configInformers,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder.WithComponentSuffix("cert-rotation-controller"),
		certRotationScale,
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

	featureUpgradeableController := featureupgradablecontroller.NewFeatureUpgradeableController(
		operatorClient,
		configInformers,
		controllerContext.EventRecorder,
	)

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
		operatorclient.TargetNamespace,
		"kube-apiserver-audit-policies",
		configInformers.Config().V1().APIServers().Lister(),
		operatorClient,
		kubeClient,
		configInformers,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		controllerContext.EventRecorder,
	)

	staleConditionsController := staleconditions.NewRemoveStaleConditionsController(
		[]string{
			// the static pod operator used to directly set these. this removes those conditions since the static pod operator was updated.
			// these can be removed in 4.5
			"Available", "Progressing",
		},
		operatorClient,
		controllerContext.EventRecorder,
	)

	kubeletVersionSkewController := kubeletversionskewcontroller.NewKubeletVersionSkewController(
		operatorClient,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
	)

	webhookSupportabilityController := webhooksupportabilitycontroller.NewWebhookSupportabilityController(
		operatorClient,
		kubeInformersForNamespaces,
		apiextensionsInformers,
		controllerContext.EventRecorder,
	)

	// register termination metrics
	terminationobserver.RegisterMetrics()

	// register config metrics
	configmetrics.Register(configInformers)

	kubeInformersForNamespaces.Start(ctx.Done())
	configInformers.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())
	migrationInformer.Start(ctx.Done())
	apiextensionsInformers.Start(ctx.Done())

	go staticPodControllers.Start(ctx)
	go resourceSyncController.Run(ctx, 1)
	go staticResourceController.Run(ctx, 1)
	go targetConfigReconciler.Run(ctx, 1)
	go nodeKubeconfigController.Run(ctx, 1)
	go configObserver.Run(ctx, 1)
	go clusterOperatorStatus.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)
	go encryptionControllers.Run(ctx, 1)
	go featureUpgradeableController.Run(ctx, 1)
	go certRotationTimeUpgradeableController.Run(ctx, 1)
	go terminationObserver.Run(ctx, 1)
	go eventWatcher.Run(ctx, 1)
	go boundSATokenSignerController.Run(ctx, 1)
	go auditPolicyController.Run(ctx, 1)
	go staleConditionsController.Run(ctx, 1)
	go connectivityCheckController.Run(ctx, 1)
	go kubeletVersionSkewController.Run(ctx, 1)
	go webhookSupportabilityController.Run(ctx, 1)

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
