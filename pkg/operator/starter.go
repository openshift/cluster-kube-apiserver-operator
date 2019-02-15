package operator

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	operatorversionedclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorv1informers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/revision"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
)

func RunOperator(ctx *controllercmd.ControllerContext) error {
	// This kube client use protobuf, do not use it for CR
	kubeClient, err := kubernetes.NewForConfig(ctx.ProtoKubeConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorversionedclient.NewForConfig(ctx.KubeConfig)
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
	operatorConfigInformers := operatorv1informers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
		"kube-system",
	)
	configInformers := configv1informers.NewSharedInformerFactory(configClient, 10*time.Minute)
	operatorClient := &operatorclient.OperatorClient{
		Informers: operatorConfigInformers,
		Client:    operatorConfigClient.OperatorV1(),
	}

	v1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-apiserver/operator-config.yaml"),
		schema.GroupVersionResource{Group: operatorv1.GroupName, Version: "v1", Resource: "kubeapiservers"},
	)

	resourceSyncController, err := resourcesynccontroller.NewResourceSyncController(
		operatorClient,
		kubeInformersForNamespaces,
		kubeClient,
		ctx.EventRecorder,
	)
	if err != nil {
		return err
	}

	configObserver := configobservercontroller.NewConfigObserver(
		operatorClient,
		operatorConfigInformers,
		kubeInformersForNamespaces,
		configInformers,
		resourceSyncController,
		ctx.EventRecorder,
	)

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		os.Getenv("IMAGE"),
		operatorConfigInformers.Operator().V1().KubeAPIServers(),
		operatorClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeInformersForNamespaces,
		operatorConfigClient.OperatorV1(),
		kubeClient,
		ctx.EventRecorder,
	)

	staticPodControllers := staticpod.NewControllers(
		operatorclient.TargetNamespace,
		"openshift-kube-apiserver",
		"kube-apiserver-pod",
		[]string{"cluster-kube-apiserver-operator", "installer"},
		[]string{"cluster-kube-apiserver-operator", "prune"},
		deploymentConfigMaps,
		deploymentSecrets,
		operatorClient,
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		kubeClient.CoreV1(),
		kubeClient,
		dynamicClient,
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace),
		kubeInformersForNamespaces.InformersFor(""),
		ctx.EventRecorder,
	)
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"kube-apiserver",
		[]configv1.ObjectReference{
			{Group: "operator.openshift.io", Resource: "kubeapiservers", Name: "cluster"},
			{Resource: "namespaces", Name: operatorclient.GlobalUserSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.GlobalMachineSpecifiedConfigNamespace},
			{Resource: "namespaces", Name: operatorclient.OperatorNamespace},
			{Resource: "namespaces", Name: operatorclient.TargetNamespace},
		},
		configClient.ConfigV1(),
		operatorClient,
		status.NewVersionGetter(),
		ctx.EventRecorder,
	)

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		kubeClient,
		operatorClient,
		kubeInformersForNamespaces,
		ctx.EventRecorder,
	)
	if err != nil {
		return err
	}

	operatorConfigInformers.Start(ctx.Done())
	kubeInformersForNamespaces.Start(ctx.Done())
	configInformers.Start(ctx.Done())

	go staticPodControllers.Run(ctx.Done())
	go resourceSyncController.Run(1, ctx.Done())
	go targetConfigReconciler.Run(1, ctx.Done())
	go configObserver.Run(1, ctx.Done())
	go clusterOperatorStatus.Run(1, ctx.Done())
	go certRotationController.Run(1, ctx.Done())

	<-ctx.Done()
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []revision.RevisionResource{
	{Name: "kube-apiserver-pod"},

	{Name: "aggregator-client-ca"},
	{Name: "client-ca"},
	{Name: "config"},
	{Name: "etcd-serving-ca"},
	{Name: "initial-sa-token-signing-certs", Optional: true},
	{Name: "kube-controller-manager-sa-token-signing-certs", Optional: true},
	{Name: "kubelet-serving-ca"},
	{Name: "oauth-metadata", Optional: true},
	{Name: "user-client-ca", Optional: true},
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []revision.RevisionResource{
	{Name: "aggregator-client"},
	{Name: "etcd-client"},
	{Name: "kubelet-client"},
	{Name: "serving-cert"},
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
