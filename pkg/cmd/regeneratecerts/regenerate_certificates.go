package regeneratecerts

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/targetconfigcontroller"

	"k8s.io/client-go/kubernetes"

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
	"github.com/openshift/library-go/pkg/config/client"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	kubecontrollermanagercertrotationcontroller "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/regeneratecerts/carry/kubecontrollermanager/certrotationcontroller"
	kubecontrollermanageroperatorclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/regeneratecerts/carry/kubecontrollermanager/operatorclient"
	kcmtargetconfigcontroller "github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/regeneratecerts/carry/kubecontrollermanager/targetconfigcontroller"
	kubeapiservercertrotationcontroller "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	kubeapiserveroperatorclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/recovery"
)

const (
	RecoveryPodKubeConfig                  = "/etc/kubernetes/static-pod-resources/recovery-kube-apiserver-pod/admin.kubeconfig"
	KubeAPIServerStaticPodFileName         = "kube-apiserver-pod.yaml"
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
	PodManifestDir        string
	StaticPodResourcesDir string
}

func NewRegenerateCertsCommand() *cobra.Command {
	o := &Options{
		PodManifestDir:        "/etc/kubernetes/manifests",
		StaticPodResourcesDir: "/etc/kubernetes/static-pod-resources",
	}

	cmd := &cobra.Command{
		Use: "regenerate-certificates",
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

	return cmd
}

func (o *Options) Complete() error {
	return nil
}

func (o *Options) Validate() error {
	return nil
}

func (o *Options) Run() error {
	ctx, cancel := watch.ContextWithOptionalTimeout(context.TODO(), 0 /*infinity*/)
	defer cancel()

	signalHandler := server.SetupSignalHandler()
	go func() {
		<-signalHandler
		cancel()
	}()

	restConfig, err := client.GetClientConfig(RecoveryPodKubeConfig, nil)
	if err != nil {
		return fmt.Errorf("failed to get kubernetes rest config: %v", err)
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	configClient, err := configeversionedclient.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %v", err)
	}

	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	kubeAPIServerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		kubeapiserveroperatorclient.GlobalUserSpecifiedConfigNamespace,
		kubeapiserveroperatorclient.GlobalMachineSpecifiedConfigNamespace,
		kubeapiserveroperatorclient.TargetNamespace,
		kubeapiserveroperatorclient.OperatorNamespace,
		"kube-system",
	)
	kubeControllerManagerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		"",
		kubecontrollermanageroperatorclient.GlobalUserSpecifiedConfigNamespace,
		kubecontrollermanageroperatorclient.GlobalMachineSpecifiedConfigNamespace,
		kubecontrollermanageroperatorclient.TargetNamespace,
		kubecontrollermanageroperatorclient.OperatorNamespace,
		"kube-system",
	)
	eventRecorder := events.NewKubeRecorder(kubeClient.CoreV1().Events(""), "fix-certs (CLI)", &corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "namespace",
		Name:       "openshift-kube-apiserver-operator",
		Namespace:  "openshift-kube-apiserver-operator",
	}) // this is a fake object-reference that should hopefully place us in the correct namespace

	// On manual request we want to rotate even if the certs are close to expiry to avoid case when some other cert becomes invalid just after.
	kubeAPIServerCertRotationController, err := kubeapiservercertrotationcontroller.NewCertRotationController(
		kubeClient,
		nil,
		configInformers,
		kubeAPIServerInformersForNamespaces,
		eventRecorder.WithComponentSuffix("cert-rotation-controller-kas"),
		0,
	)
	if err != nil {
		return err
	}
	kubeControllerManagerCertRotationController, err := kubecontrollermanagercertrotationcontroller.NewCertRotationController(
		kubeClient.CoreV1(),
		kubeClient.CoreV1(),
		kubeControllerManagerInformersForNamespaces,
		eventRecorder.WithComponentSuffix("cert-rotation-controller-kcm"),
		0,
	)
	if err != nil {
		return err
	}

	// you can't start informers until after the resources have been requested
	configInformers.Start(ctx.Done())
	kubeAPIServerInformersForNamespaces.Start(ctx.Done())
	kubeControllerManagerInformersForNamespaces.Start(ctx.Done())

	// wait until the controllers are ready
	kubeAPIServerCertRotationController.WaitForReady(ctx.Done())
	kubeControllerManagerCertRotationController.WaitForReady(ctx.Done())
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	klog.Info("Refreshing certificates.")
	if err := kubeAPIServerCertRotationController.RunOnce(); err != nil {
		return err
	}
	if err := kubeControllerManagerCertRotationController.RunOnce(); err != nil {
		return err
	}
	klog.Info("Certificates refreshed.")

	klog.Info("Refreshing derivative resources.")
	// TODO make the method take clients which can be cached, just no time in 4.1; ugly, but this is lister driven and we want to be fairly sure we observe the change
	time.Sleep(2 * time.Second)
	if _, _, err = kcmtargetconfigcontroller.ManageCSRIntermediateCABundle(kubeControllerManagerInformersForNamespaces.SecretLister(), kubeClient.CoreV1(), eventRecorder); err != nil {
		return err
	}
	// TODO make the method take clients which can be cached, just no time in 4.1; ugly, but this is lister driven and we want to be fairly sure we observe the change
	time.Sleep(2 * time.Second)
	if _, _, err = kcmtargetconfigcontroller.ManageCSRCABundle(kubeControllerManagerInformersForNamespaces.ConfigMapLister(), kubeClient.CoreV1(), eventRecorder); err != nil {
		return err
	}
	// TODO make the method take clients which can be cached, just no time in 4.1; ugly, but this is lister driven and we want to be fairly sure we observe the change
	time.Sleep(2 * time.Second)
	if _, _, err = kcmtargetconfigcontroller.ManageCSRSigner(kubeControllerManagerInformersForNamespaces.SecretLister(), kubeClient.CoreV1(), eventRecorder); err != nil {
		return err
	}
	// copy to shared location so that the "normal" client-ca building can be used
	if _, _, err := resourceapply.SyncConfigMap(
		kubeClient.CoreV1(), eventRecorder,
		kubecontrollermanageroperatorclient.OperatorNamespace, "csr-controller-ca",
		kubecontrollermanageroperatorclient.GlobalMachineSpecifiedConfigNamespace, "csr-controller-ca", []metav1.OwnerReference{}); err != nil {
		return err
	}
	// TODO make the method take clients which can be cached, just no time in 4.1; ugly, but this is lister driven and we want to be fairly sure we observe the change
	time.Sleep(2 * time.Second)
	if _, _, err := targetconfigcontroller.ManageClientCABundle(kubeAPIServerInformersForNamespaces.ConfigMapLister(), kubeClient.CoreV1(), eventRecorder); err != nil {
		return err
	}
	klog.Info("Derivative resources refreshed.")

	kubeApiserverManifestPath := filepath.Join(o.PodManifestDir, KubeAPIServerStaticPodFileName)
	kubeApiserverPod, err := recovery.ReadManifestToV1Pod(kubeApiserverManifestPath)
	if err != nil {
		return fmt.Errorf("failed to read kube-apiserver manifest at %q: %v", kubeApiserverManifestPath, err)
	}
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
		return fmt.Errorf("failed to find kube-controller-manager resource dir: %v", err)
	}
	kubeControllerManagerCertDir, err := recovery.GetVolumeHostPathPath("cert-dir", kubeControllerManagerPod.Spec.Volumes)
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
			namespace:   kubecontrollermanageroperatorclient.TargetNamespace,
			toplevelDir: kubeControllerManagerCertDir,
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
	if kubeApiserverPod.Annotations == nil {
		kubeApiserverPod.Annotations = map[string]string{}
	}
	kubeApiserverPod.Annotations["force-triggered-by-fix-certs-at"] = timestamp
	if kubeControllerManagerPod.Annotations == nil {
		kubeControllerManagerPod.Annotations = map[string]string{}
	}
	kubeControllerManagerPod.Annotations["force-triggered-by-fix-certs-at"] = timestamp
	if kubeSchedulerPod.Annotations == nil {
		kubeSchedulerPod.Annotations = map[string]string{}
	}
	kubeSchedulerPod.Annotations["force-triggered-by-fix-certs-at"] = timestamp

	// Force restart kube-apiserver just in case (even all its cert files are dynamically reloaded)
	kubeApiserverBytes, err := yaml.Marshal(kubeApiserverPod)
	if err != nil {
		return fmt.Errorf("failed to marshal kube-apiserver pod: %v", err)
	}

	err = ioutil.WriteFile(kubeApiserverManifestPath, kubeApiserverBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write kube-apiserver pod manifest %q: %v", kubeApiserverManifestPath, err)
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
