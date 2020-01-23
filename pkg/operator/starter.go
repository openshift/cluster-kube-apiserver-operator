package operator

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"

	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/encryption"
	"github.com/openshift/library-go/pkg/operator/encryption/controllers/migrators"
	encryptiondeployer "github.com/openshift/library-go/pkg/operator/encryption/deployer"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/revision"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationtimeupgradeablecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configmetrics"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/featureupgradablecontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/terminationobserver"
)

func RunOperator(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	// This kube client use protobuf, do not use it for CR
	kubeClient, err := kubernetes.NewForConfig(controllerContext.ProtoKubeConfig)
	if err != nil {
		return err
	}
	migrationClientConfig := dynamic.ConfigFor(controllerContext.KubeConfig)
	migrationClientConfig.Burst = 40
	migrationClientConfig.QPS = 30
	dynamicClientForMigration, err := dynamic.NewForConfig(migrationClientConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(controllerContext.KubeConfig)
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
		"openshift-etcd",
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

	staticResourceController := staticresourcecontroller.NewStaticResourceController(
		"KubeAPIServerStaticResources",
		v410_00_assets.Asset,
		[]string{
			"v4.1.0/kube-apiserver/ns.yaml",
			"v4.1.0/kube-apiserver/svc.yaml",
			"v4.1.0/kube-apiserver/kubeconfig-cm.yaml",
			"v4.1.0/kube-apiserver/localhost-recovery-client-crb.yaml",
			"v4.1.0/kube-apiserver/localhost-recovery-sa.yaml",
			"v4.1.0/kube-apiserver/localhost-recovery-token.yaml",
		},
		(&resourceapply.ClientHolder{}).WithKubernetes(kubeClient),
		operatorClient,
		controllerContext.EventRecorder,
	).
		AddInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer()).
		AddInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer()).
		AddInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ServiceAccounts().Informer()).
		AddInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Services().Informer()).
		AddInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Rbac().V1().ClusterRoleBindings().Informer()).
		AddNamespaceInformer(kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Namespaces().Informer(), operatorclient.TargetNamespace)

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		os.Getenv("IMAGE"),
		os.Getenv("OPERATOR_IMAGE"),
		operatorClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
	)

	// don't change any versions until we sync
	versionRecorder := status.NewVersionGetter()
	clusterOperator, err := configClient.ConfigV1().ClusterOperators().Get("kube-apiserver", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	for _, version := range clusterOperator.Status.Versions {
		versionRecorder.SetVersion(version.Name, version.Version)
	}
	versionRecorder.SetVersion("raw-internal", status.VersionForOperatorFromEnv())

	staticPodControllers, err := staticpod.NewBuilder(operatorClient, kubeClient, kubeInformersForNamespaces).
		WithEvents(controllerContext.EventRecorder).
		WithInstaller([]string{"cluster-kube-apiserver-operator", "installer"}).
		WithPruning([]string{"cluster-kube-apiserver-operator", "prune"}, "kube-apiserver-pod").
		WithResources(operatorclient.TargetNamespace, "kube-apiserver", RevisionConfigMaps, RevisionSecrets).
		WithCerts("kube-apiserver-certs", CertConfigMaps, CertSecrets).
		WithVersioning(operatorclient.OperatorNamespace, "kube-apiserver", versionRecorder).
		ToControllers()
	if err != nil {
		return err
	}

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"kube-apiserver",
		[]configv1.ObjectReference{
			{Group: "operator.openshift.io", Resource: "kubeapiservers", Name: "cluster"},
			{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
			{Resource: "namespaces", Name: operatorclient.GlobalUserSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.GlobalMachineSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.OperatorNamespace},
			{Resource: "namespaces", Name: operatorclient.TargetNamespace},
		},
		configClient.ConfigV1(),
		configInformers.Config().V1().ClusterOperators(),
		operatorClient,
		versionRecorder,
		controllerContext.EventRecorder,
	)

	certRotationScale, err := certrotation.GetCertRotationScale(kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
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
	deployer, err := encryptiondeployer.NewRevisionLabelPodDeployer("revision", operatorclient.TargetNamespace, kubeInformersForNamespaces, resourceSyncController, kubeClient.CoreV1(), kubeClient.CoreV1(), staticPodNodeProvider)
	if err != nil {
		return err
	}
	migrator := migrators.NewInProcessMigrator(dynamicClientForMigration, kubeClient.Discovery())

	encryptionControllers, err := encryption.NewControllers(
		operatorclient.TargetNamespace,
		deployer,
		migrator,
		operatorClient,
		configClient.ConfigV1().APIServers(),
		configInformers.Config().V1().APIServers(),
		kubeInformersForNamespaces,
		kubeClient.CoreV1(),
		controllerContext.EventRecorder,
		schema.GroupResource{Group: "", Resource: "secrets"},
		schema.GroupResource{Group: "", Resource: "configmaps"},
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

	// register termination metrics
	terminationobserver.RegisterMetrics()

	// register config metrics
	configmetrics.Register(configInformers)

	kubeInformersForNamespaces.Start(ctx.Done())
	configInformers.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())

	go staticPodControllers.Run(ctx, 1)
	go resourceSyncController.Run(ctx, 1)
	go staticResourceController.Run(ctx, 1)
	go targetConfigReconciler.Run(1, ctx.Done())
	go configObserver.Run(ctx, 1)
	go clusterOperatorStatus.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)
	go encryptionControllers.Run(ctx.Done())
	go featureUpgradeableController.Run(1, ctx.Done())
	go certRotationTimeUpgradeableController.Run(1, ctx.Done())
	go terminationObserver.Run(ctx, 1)

	<-ctx.Done()
	return fmt.Errorf("stopped")
}

// RevisionConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var RevisionConfigMaps = []revision.RevisionResource{
	{Name: "kube-apiserver-pod"},

	{Name: "config"},
	{Name: "kube-apiserver-cert-syncer-kubeconfig"},
	{Name: "oauth-metadata", Optional: true},
	{Name: "cloud-config", Optional: true},

	// these need to removed, but if we remove them now, the cluster will die because we don't reload them yet
	{Name: "etcd-serving-ca"},
	{Name: "kube-apiserver-server-ca", Optional: true},
	{Name: "kubelet-serving-ca"},
	{Name: "sa-token-signing-certs"},
}

// RevisionSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var RevisionSecrets = []revision.RevisionResource{
	// these need to removed, but if we remove them now, the cluster will die because we don't reload them yet
	{Name: "etcd-client"},
	{Name: "kubelet-client"},
	// etcd encryption
	{Name: "encryption-config", Optional: true},

	// this needs to be revisioned as certsyncer's kubeconfig isn't wired to be live reloaded, nor will be autorecovery
	{Name: "localhost-recovery-serving-certkey"},
	{Name: "localhost-recovery-client-token"},
}

var CertConfigMaps = []revision.RevisionResource{
	{Name: "aggregator-client-ca"},
	{Name: "client-ca"},

	// this is a copy of trusted-ca-bundle CM without the injection annotations
	{Name: "trusted-ca-bundle", Optional: true},
}

var CertSecrets = []revision.RevisionResource{
	{Name: "aggregator-client"},
	{Name: "localhost-serving-cert-certkey"},
	{Name: "service-network-serving-certkey"},
	{Name: "external-loadbalancer-serving-certkey"},
	{Name: "internal-loadbalancer-serving-certkey"},

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
