package operator

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	managedConfigNamespace = "openshift-config-managed"
)

func RunOperator(ctx *controllercmd.ControllerContext) error {
	kubeClient, err := kubernetes.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersClusterScoped := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	kubeInformersForUserSpecifiedGlobalConfigNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(userSpecifiedGlobalConfigNamespace))
	kubeInformersForMachineSpecifiedGlobalConfigNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(machineSpecifiedGlobalConfigNamespace))
	kubeInformersForOpenshiftKubeAPIServerOperatorNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(operatorNamespace))
	kubeInformersForOpenshiftKubeAPIServerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(targetNamespaceName))
	kubeInformersForKubeSystemNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system"))
	kubeInformersForOpenshiftConfigManaged := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(managedConfigNamespace))
	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	staticPodOperatorClient := &staticPodOperatorClient{
		informers: operatorConfigInformers,
		client:    operatorConfigClient.KubeapiserverV1alpha1(),
	}

	v1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubeapiserveroperatorconfigs"},
	)

	configObserver := configobservercontroller.NewConfigObserver(
		staticPodOperatorClient,
		operatorConfigInformers,
		kubeInformersForKubeSystemNamespace,
		configInformers,
		ctx.EventRecorder,
	)
	resourceSyncController := resourcesynccontroller.NewResourceSyncController(
		staticPodOperatorClient,
		map[string]informers.SharedInformerFactory{
			userSpecifiedGlobalConfigNamespace:    kubeInformersForUserSpecifiedGlobalConfigNamespace,
			machineSpecifiedGlobalConfigNamespace: kubeInformersForMachineSpecifiedGlobalConfigNamespace,
			operatorNamespace:                     kubeInformersForOpenshiftKubeAPIServerOperatorNamespace,
			targetNamespaceName:                   kubeInformersForOpenshiftKubeAPIServerNamespace,
			"kube-system":                         kubeInformersForKubeSystemNamespace,
		},
		kubeClient,
		ctx.EventRecorder,
	)
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespaceName, Name: "etcd-serving-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: etcdNamespaceName, Name: "etcd-serving-ca"},
	); err != nil {
		return err
	}
	if err := resourceSyncController.SyncSecret(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespaceName, Name: "etcd-client"},
		resourcesynccontroller.ResourceLocation{Namespace: etcdNamespaceName, Name: "etcd-client"},
	); err != nil {
		return err
	}
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespaceName, Name: "aggregator-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: machineSpecifiedGlobalConfigNamespace, Name: "aggregator-client-ca"},
	); err != nil {
		return err
	}
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorNamespace, Name: "initial-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: userSpecifiedGlobalConfigNamespace, Name: "initial-client-ca"},
	); err != nil {
		return err
	}
	// this ca bundle contains certs used to sign CSRs (kubelet serving and client certificates)
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorNamespace, Name: "csr-controller-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: machineSpecifiedGlobalConfigNamespace, Name: "csr-controller-ca"},
	); err != nil {
		return err
	}
	// this ca bundle contains certs used by the kube-apiserver to verify client certs
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: machineSpecifiedGlobalConfigNamespace, Name: "kube-apiserver-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: targetNamespaceName, Name: "client-ca"},
	); err != nil {
		return err
	}
	targetConfigReconciler := NewTargetConfigReconciler(
		os.Getenv("IMAGE"),
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs(),
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
		ctx.EventRecorder,
	)

	staticPodControllers := staticpod.NewControllers(
		targetNamespaceName,
		"openshift-kube-apiserver",
		[]string{"cluster-kube-apiserver-operator", "installer"},
		deploymentConfigMaps,
		deploymentSecrets,
		staticPodOperatorClient,
		kubeClient,
		kubeInformersForOpenshiftKubeAPIServerNamespace,
		kubeInformersClusterScoped,
		ctx.EventRecorder,
	)
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-apiserver-operator",
		configClient.ConfigV1(),
		staticPodOperatorClient,
		ctx.EventRecorder,
	)

	// start cert rotation controllers
	aggregatorProxyClientCertController := certrotation.NewClientCertRotationController(
		"AggregatorProxyClientCert",
		operatorNamespace,
		1*24*time.Hour,
		0.5,
		"aggregator-client-signer",
		managedConfigNamespace,
		"aggregator-client-ca",
		targetNamespaceName,
		1*24*time.Hour,
		0.75,
		"aggregator-client",
		&user.DefaultInfo{Name: "system:openshift-aggregator"},
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Core().V1().Secrets(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().ConfigMaps(),
		kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets(),
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		ctx.EventRecorder,
	)
	controllerClientCertController := certrotation.NewClientCertRotationController(
		"KubeControllerManagerClient",
		operatorNamespace,
		1*24*time.Hour,
		0.5,
		"managed-kube-apiserver-client-signer",
		managedConfigNamespace,
		"managed-kube-apiserver-client-ca-bundle",
		managedConfigNamespace,
		1*24*time.Hour,
		0.75,
		"kube-controller-manager-client-cert-key",
		&user.DefaultInfo{},
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Core().V1().Secrets(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().ConfigMaps(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().Secrets(),
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		ctx.EventRecorder,
	)
	schedulerClientCertController := certrotation.NewClientCertRotationController(
		"KubeSchedulerClient",
		operatorNamespace,
		1*24*time.Hour,
		0.5,
		"managed-kube-apiserver-client-signer",
		managedConfigNamespace,
		"managed-kube-apiserver-client-ca-bundle",
		managedConfigNamespace,
		1*24*time.Hour,
		0.75,
		"kube-scheduler-client-cert-key",
		&user.DefaultInfo{},
		kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Core().V1().Secrets(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().ConfigMaps(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().Secrets(),
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		ctx.EventRecorder,
	)
	loopbackServingCertController := certrotation.NewServingCertRotationController(
		"ManagedKubeAPIServerServingCert",
		managedConfigNamespace,
		1*24*time.Hour,
		0.5,
		"managed-kube-apiserver-serving-cert-signer",
		managedConfigNamespace,
		"managed-kube-apiserver-serving-cert-ca-bundle",
		managedConfigNamespace,
		1*24*time.Hour,
		0.75,
		"managed-kube-apiserver-serving-cert-key",
		[]string{"localhost", "127.0.0.1", "kubernetes.default.svc"},
		nil,
		kubeInformersForOpenshiftConfigManaged.Core().V1().Secrets(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().ConfigMaps(),
		kubeInformersForOpenshiftConfigManaged.Core().V1().Secrets(),
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		ctx.EventRecorder,
	)

	operatorConfigInformers.Start(ctx.StopCh)
	kubeInformersClusterScoped.Start(ctx.StopCh)
	kubeInformersForUserSpecifiedGlobalConfigNamespace.Start(ctx.StopCh)
	kubeInformersForMachineSpecifiedGlobalConfigNamespace.Start(ctx.StopCh)
	kubeInformersForOpenshiftKubeAPIServerOperatorNamespace.Start(ctx.StopCh)
	kubeInformersForOpenshiftKubeAPIServerNamespace.Start(ctx.StopCh)
	kubeInformersForKubeSystemNamespace.Start(ctx.StopCh)
	kubeInformersForOpenshiftConfigManaged.Start(ctx.StopCh)
	configInformers.Start(ctx.StopCh)

	go staticPodControllers.Run(ctx.StopCh)
	go resourceSyncController.Run(1, ctx.StopCh)
	go targetConfigReconciler.Run(1, ctx.StopCh)
	go configObserver.Run(1, ctx.StopCh)
	go clusterOperatorStatus.Run(1, ctx.StopCh)
	go aggregatorProxyClientCertController.Run(1, ctx.StopCh)
	go controllerClientCertController.Run(1, ctx.StopCh)
	go schedulerClientCertController.Run(1, ctx.StopCh)
	go loopbackServingCertController.Run(1, ctx.StopCh)

	<-ctx.StopCh
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []string{
	"kube-apiserver-pod",
	"config",
	"aggregator-client-ca",
	"client-ca",
	"etcd-serving-ca",
	"kubelet-serving-ca",
	"sa-token-signing-certs",
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []string{
	"aggregator-client",
	"etcd-client",
	"kubelet-client",
	"serving-cert",
}
