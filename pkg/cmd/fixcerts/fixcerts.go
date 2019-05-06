package fixcerts

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
	"k8s.io/apiserver/pkg/server"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/watch"
	"k8s.io/klog"

	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configexternalinformers "github.com/openshift/client-go/config/informers/externalversions"
	operatorversionedclient "github.com/openshift/client-go/operator/clientset/versioned"
	operatorexternalinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	kubecontrollermanagercertrotationcontroller "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/fixcerts/carry/kubecontrollermanager/certrotationcontroller"
	kubecontrollermanageroperatorclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/fixcerts/carry/kubecontrollermanager/operatorclient"
	kubeapiservercertrotationcontroller "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	kubeapiserveroperatorclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

const (
	KubeControllerManagerStaticPodFileName = "kube-controller-manager-pod.yaml"
	KubeSchedulerStaticPodFileName         = "kube-scheduler-pod.yaml"

	secretsType    = "secrets"
	configmapsType = "configmaps"
)

var (
	Scheme = runtime.NewScheme()
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	utilruntime.Must(corev1.AddToScheme(Scheme))
}

type Options struct {
	// TODO: merge with CreateOptions
	PodManifestDir        string
	StaticPodResourcesDir string
	Timeout               time.Duration
}

func NewCommand() *cobra.Command {
	o := &Options{
		PodManifestDir:        "/etc/kubernetes/manifests",
		StaticPodResourcesDir: "/etc/kubernetes/static-pod-resources",
		Timeout:               5 * time.Minute,
	}

	cmd := &cobra.Command{
		Use: "fix-certs",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := o.Complete()
			if err != nil {
				return err
			}

			err = o.Validate()
			if err != nil {
				return err
			}

			err = o.Run()
			if err != nil {
				return err
			}

			return nil
		},

		SilenceErrors: true,
		SilenceUsage:  true,
	}

	cmd.Flags().DurationVar(&o.Timeout, "timeout", o.Timeout, "timeout for the command, 0 means infinite")

	return cmd
}

func (o *Options) Complete() error {
	return nil
}

func (o *Options) Validate() error {
	return nil
}

func (o *Options) Run() error {
	ctx, cancel := watch.ContextWithOptionalTimeout(context.TODO(), o.Timeout)
	defer cancel()

	signalHandler := server.SetupSignalHandler()
	go func() {
		<-signalHandler
		cancel()
	}()

	recoveryApiserver := &recovery.Apiserver{
		PodManifestDir:        o.PodManifestDir,
		StaticPodResourcesDir: o.StaticPodResourcesDir,
	}

	err := recoveryApiserver.Create()
	if err != nil {
		return fmt.Errorf("failed to create recovery recoveryApiserver: %v", err)
	}
	defer recoveryApiserver.Destroy()

	klog.Info("Waiting for recovery recoveryApiserver to come up")
	err = recoveryApiserver.WaitForHealthz(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for recovery recoveryApiserver to be ready: %v", err)
	}
	klog.Info("Recovery recoveryApiserver is up")

	// Run cert recovery

	// We already have kubeClient created
	kubeClient, err := recoveryApiserver.GetKubeClientset()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes clientset: %v", err)
	}

	restConfig, err := recoveryApiserver.RestConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes rest config: %v", err)
	}

	operatorConfigClient, err := operatorversionedclient.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	configClient, err := configeversionedclient.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %v", err)
	}

	certRotationCtx, certRotationCtxCancel := context.WithCancel(ctx)
	defer certRotationCtxCancel()

	certRotationWg := sync.WaitGroup{}

	operatorConfigInformers := operatorexternalinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()
		operatorConfigInformers.Start(certRotationCtx.Done())
	}()

	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()
		configInformers.Start(certRotationCtx.Done())
	}()

	kubeApiserverOperatorClient := &kubeapiserveroperatorclient.OperatorClient{
		Informers: operatorConfigInformers,
		Client:    operatorConfigClient.OperatorV1(),
	}

	kubeControllerManagerOperatorClient := &kubecontrollermanageroperatorclient.OperatorClient{
		Informers: operatorConfigInformers,
		Client:    operatorConfigClient.OperatorV1(),
	}

	kubeApiserverInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		kubeapiserveroperatorclient.GlobalUserSpecifiedConfigNamespace,
		kubeapiserveroperatorclient.GlobalMachineSpecifiedConfigNamespace,
		kubeapiserveroperatorclient.TargetNamespace,
		kubeapiserveroperatorclient.OperatorNamespace,
		"kube-system",
	)
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()
		kubeApiserverInformersForNamespaces.Start(certRotationCtx.Done())
	}()

	kubeControllerManagerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		kubecontrollermanageroperatorclient.GlobalUserSpecifiedConfigNamespace,
		kubecontrollermanageroperatorclient.GlobalMachineSpecifiedConfigNamespace,
		kubecontrollermanageroperatorclient.TargetNamespace,
		kubecontrollermanageroperatorclient.OperatorNamespace,
		"kube-system",
	)
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()
		kubeControllerManagerInformersForNamespaces.Start(certRotationCtx.Done())
	}()

	eventRecorder := events.NewKubeRecorder(kubeClient.CoreV1().Events(""), "fix-certs (CLI)", &corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "namespace",
		Name:       "kube-system",
	}) // fake

	kubeApiserverCertRotationController, err := kubeapiservercertrotationcontroller.NewCertRotationController(
		kubeClient,
		kubeApiserverOperatorClient,
		configInformers,
		kubeApiserverInformersForNamespaces,
		eventRecorder.WithComponentSuffix("cert-rotation-controller-kas"),
		0,
	)
	if err != nil {
		return err
	}
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()

		kubeApiserverCertRotationController.Run(1, certRotationCtx.Done())
	}()

	kubeControllerManagerCertRotationController, err := kubecontrollermanagercertrotationcontroller.NewCertRotationController(
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		kubeControllerManagerOperatorClient,
		kubeControllerManagerInformersForNamespaces,
		eventRecorder.WithComponentSuffix("cert-rotation-controller-kcm"),
		0,
	)
	if err != nil {
		return err
	}
	certRotationWg.Add(1)
	go func() {
		defer certRotationWg.Done()

		kubeControllerManagerCertRotationController.Run(1, certRotationCtx.Done())
	}()

	klog.Info("Waiting for certs to be refreshed...")
	// FIXME: wait for valid certs
	// time.Sleep(5*time.Minute)
	time.Sleep(30 * time.Second)
	klog.Info("Certificates refreshed.")

	klog.V(1).Info("Stopping CertRotationController...")
	certRotationCtxCancel()
	certRotationWg.Wait()
	klog.V(1).Info("Stopped CertRotationController...")

	kubeApiserverPod := recoveryApiserver.GetKubeApiserverStaticPod()
	kubeApiserverCertsDir, err := recovery.GetVolumeHostPathPath("cert-dir", kubeApiserverPod.Spec.Volumes)
	if err != nil {
		return fmt.Errorf("failed to find kube-recoveryApiserver certs dir: %v", err)
	}

	kubeControllerManagerManifest := filepath.Join(o.PodManifestDir, KubeControllerManagerStaticPodFileName)
	kubeControllerManagerPod, err := recovery.ReadManifestToV1Pod(kubeControllerManagerManifest)
	if err != nil {
		return fmt.Errorf("failed to read kube-controller-manager manifest at %q: %v", kubeControllerManagerManifest, err)
	}
	kubeControllerManagerResourceDir, err := recovery.GetVolumeHostPathPath("resource-dir", kubeControllerManagerPod.Spec.Volumes)
	if err != nil {
		return fmt.Errorf("failed to find kube-controller-manager certs dir: %v", err)
	}

	kubeSchedulerManifest := filepath.Join(o.PodManifestDir, KubeSchedulerStaticPodFileName)
	kubeSchedulerPod, err := recovery.ReadManifestToV1Pod(kubeSchedulerManifest)
	if err != nil {
		return fmt.Errorf("failed to read kube-scheduler manifest at %q: %v", kubeSchedulerManifest, err)
	}
	kubeSchedulerResourceDir, err := recovery.GetVolumeHostPathPath("resource-dir", kubeSchedulerPod.Spec.Volumes)
	if err != nil {
		return fmt.Errorf("failed to find kube-scheduler certs dir: %v", err)
	}

	definitions := []struct {
		objectType  string
		name        string
		namespace   string
		toplevelDir string
	}{
		// Fix kube-recoveryApiserver serving certs
		{
			objectType:  secretsType,
			name:        "service-network-serving-certkey",
			namespace:   kubeapiserveroperatorclient.TargetNamespace,
			toplevelDir: kubeApiserverCertsDir,
		},
		{
			objectType:  configmapsType,
			name:        "client-ca",
			namespace:   kubeapiserveroperatorclient.TargetNamespace,
			toplevelDir: kubeApiserverCertsDir,
		},
		{
			objectType:  secretsType,
			name:        "localhost-serving-cert-certkey",
			namespace:   kubeapiserveroperatorclient.TargetNamespace,
			toplevelDir: kubeApiserverCertsDir,
		},
		{
			objectType:  secretsType,
			name:        "internal-loadbalancer-serving-certkey",
			namespace:   kubeapiserveroperatorclient.TargetNamespace,
			toplevelDir: kubeApiserverCertsDir,
		},

		// Fix controller-manager certs
		{
			objectType:  secretsType,
			name:        "kube-controller-manager-client-cert-key",
			namespace:   kubeapiserveroperatorclient.GlobalMachineSpecifiedConfigNamespace,
			toplevelDir: kubeControllerManagerResourceDir,
		},
		{
			objectType:  secretsType,
			name:        "csr-signer",
			namespace:   kubecontrollermanageroperatorclient.OperatorNamespace,
			toplevelDir: kubeControllerManagerResourceDir,
		},

		// Fix scheduler certs
		{
			objectType:  secretsType,
			name:        "kube-scheduler-client-cert-key",
			namespace:   kubeapiserveroperatorclient.GlobalMachineSpecifiedConfigNamespace,
			toplevelDir: kubeSchedulerResourceDir,
		},
	}
	for _, def := range definitions {
		data := map[string][]byte{}
		switch def.objectType {
		case secretsType:
			klog.V(2).Infof("Getting secret '%s/%s'", def.namespace, def.name)
			secret, err := kubeClient.CoreV1().Secrets(def.namespace).Get(def.name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			data = secret.Data

		case configmapsType:
			klog.V(2).Infof("Getting configmap '%s/%s'", def.namespace, def.name)
			configMap, err := kubeClient.CoreV1().ConfigMaps(def.namespace).Get(def.name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			for k, v := range configMap.Data {
				data[k] = []byte(v)
			}
		default:
			return fmt.Errorf("unknown object type %q", def.objectType)
		}

		dir := filepath.Join(def.toplevelDir, def.objectType, def.name)
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return fmt.Errorf("failed to create dir %q: %v", dir, err)
		}

		for name, bytes := range data {
			filePath := filepath.Join(dir, name)
			err := recovery.EnsureFileContent(filePath, bytes)
			if err != nil {
				return fmt.Errorf("failed to write file %q: %v", filePath, err)
			}
		}
	}

	timestamp := time.Now().Format(time.RFC3339)
	kubeControllerManagerPod.Annotations["force-triggered-by-fix-certs-at"] = timestamp
	kubeSchedulerPod.Annotations["force-triggered-by-fix-certs-at"] = timestamp

	// Force restart kube-recoveryApiserver just in case (even all its cert files are dynamically reloaded)
	kubeApiserverManifestPath := recoveryApiserver.KubeApiserverManifestPath()
	kubeApiserverBytes, err := yaml.Marshal(kubeApiserverPod)
	if err != nil {
		return fmt.Errorf("failed to marshal kube-recoveryApiserver pod: %v", err)
	}

	err = ioutil.WriteFile(kubeApiserverManifestPath, kubeApiserverBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write kube-recoveryApiserver pod manifest %q: %v", kubeApiserverManifestPath, err)
	}

	// Force restart kube-controller-manager
	kubeControllerManagerPodBytes, err := yaml.Marshal(kubeControllerManagerPod)
	if err != nil {
		return fmt.Errorf("failed to marshal kube-controller-manager pod: %v", err)
	}

	err = ioutil.WriteFile(kubeControllerManagerManifest, kubeControllerManagerPodBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write kube-controller-manager pod manifest %q: %v", kubeControllerManagerManifest, err)
	}

	// Force restart kube-scheduler
	kubeSchedulerPodBytes, err := yaml.Marshal(kubeSchedulerPod)
	if err != nil {
		return fmt.Errorf("failed to marshal kube-scheduler pod: %v", err)
	}

	err = ioutil.WriteFile(kubeSchedulerManifest, kubeSchedulerPodBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write kube-scheduler pod manifest %q: %v", kubeSchedulerManifest, err)
	}

	return nil
}
