package targetconfigcontroller

import (
	"crypto/x509"
	"fmt"
	"reflect"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/workqueue"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorconfiginformerv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions/kubeapiserver/v1alpha1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const workQueueKey = "key"

type TargetConfigReconciler struct {
	targetImagePullSpec string

	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface

	kubeClient                          kubernetes.Interface
	configMapListerForOperatorNamespace corev1listers.ConfigMapLister
	eventRecorder                       events.Recorder

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewTargetConfigReconciler(
	targetImagePullSpec string,
	operatorConfigInformer operatorconfiginformerv1alpha1.KubeAPIServerOperatorConfigInformer,
	kubeInformersForOpenshiftKubeAPIServerNamespace informers.SharedInformerFactory,
	kubeInformersForOperatorNamespace informers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) *TargetConfigReconciler {
	c := &TargetConfigReconciler{
		targetImagePullSpec: targetImagePullSpec,

		operatorConfigClient:                operatorConfigClient,
		kubeClient:                          kubeClient,
		configMapListerForOperatorNamespace: kubeInformersForOperatorNamespace.Core().V1().ConfigMaps().Lister(),
		eventRecorder:                       eventRecorder,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "TargetConfigReconciler"),
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Rbac().V1().Roles().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Rbac().V1().RoleBindings().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().ServiceAccounts().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Services().Informer().AddEventHandler(c.eventHandler())

	// we react to some config changes
	kubeInformersForOperatorNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())

	return c
}

func (c TargetConfigReconciler) sync() error {
	operatorConfig, err := c.operatorConfigClient.KubeAPIServerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	operatorConfigOriginal := operatorConfig.DeepCopy()

	switch operatorConfig.Spec.ManagementState {
	case operatorv1.Unmanaged:
		return nil

	case operatorv1.Removed:
		// TODO probably just fail
		return nil
	}

	// block until config is obvserved
	if len(operatorConfig.Spec.ObservedConfig.Raw) == 0 {
		glog.Info("Waiting for observed configuration to be available")
		return nil
	}

	requeue, err := createTargetConfig(c, c.eventRecorder, operatorConfig)
	if requeue && err == nil {
		return fmt.Errorf("synthetic requeue request")
	}

	if err != nil {
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
				Type:    operatorv1.OperatorStatusTypeFailing,
				Status:  operatorv1.ConditionTrue,
				Reason:  "StatusUpdateError",
				Message: err.Error(),
			})
			if _, updateError := c.operatorConfigClient.KubeAPIServerOperatorConfigs().UpdateStatus(operatorConfig); updateError != nil {
				glog.Error(updateError)
			}
		}
		return err
	}

	return nil
}

// createTargetConfig takes care of creation of valid resources in a fixed name.  These are inputs to other control loops.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createTargetConfig(c TargetConfigReconciler, recorder events.Recorder, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig) (bool, error) {
	operatorConfigOriginal := operatorConfig.DeepCopy()
	errors := []error{}

	directResourceResults := resourceapply.ApplyDirectly(c.kubeClient, c.eventRecorder, v311_00_assets.Asset,
		"v3.11.0/kube-apiserver/ns.yaml",
		"v3.11.0/kube-apiserver/svc.yaml",
	)

	for _, currResult := range directResourceResults {
		if currResult.Error != nil {
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
		}
	}

	_, _, err := manageKubeApiserverConfig(c.kubeClient.CoreV1(), recorder, operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/config", err))
	}
	_, _, err = managePod(c.kubeClient.CoreV1(), recorder, operatorConfig, c.targetImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-apiserver-pod", err))
	}
	_, _, err = manageClientCABundle(c.configMapListerForOperatorNamespace, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/client-ca", err))
	}

	if len(errors) > 0 {
		v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
			Type:    "TargetConfigReconcilerFailing",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: v1helpers.NewMultiLineAggregate(errors).Error(),
		})
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			_, updateError := c.operatorConfigClient.KubeAPIServerOperatorConfigs().UpdateStatus(operatorConfig)
			return true, updateError
		}
		return true, nil
	}

	v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
		Type:   "TargetConfigReconcilerFailing",
		Status: operatorv1.ConditionFalse,
	})
	if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
		_, updateError := c.operatorConfigClient.KubeAPIServerOperatorConfigs().UpdateStatus(operatorConfig)
		if updateError != nil {
			return true, updateError
		}
	}

	return false, nil
}

func manageKubeApiserverConfig(client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-apiserver/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, operatorConfig.Spec.ObservedConfig.Raw, operatorConfig.Spec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func managePod(client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig, imagePullSpec string) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/pod.yaml"))
	required.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	if len(imagePullSpec) > 0 {
		required.Spec.Containers[0].Image = imagePullSpec
	}
	required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 2))

	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorConfig.Spec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()
	return resourceapply.ApplyConfigMap(client, recorder, configMap)
}

func manageClientCABundle(lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	inputs := []resourcesynccontroller.ResourceLocation{
		// this is from the installer and contains the value they think we should have
		{Namespace: operatorclient.OperatorNamespace, Name: "initial-client-ca"},
		// this is from kube-controller-manager and indicates the ca-bundle.crt to verify their signatures (kubelet client certs)
		{Namespace: operatorclient.OperatorNamespace, Name: "csr-controller-ca"},
		// this bundle is what this operator uses to mint new client certs it directly manages
		{Namespace: operatorclient.OperatorNamespace, Name: "managed-kube-apiserver-client-ca-bundle"},
	}

	certificates := []*x509.Certificate{}
	for _, input := range inputs {
		inputConfigMap, err := lister.ConfigMaps(input.Namespace).Get(input.Name)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, false, err
		}

		inputContent := inputConfigMap.Data["ca-bundle.crt"]
		if len(inputContent) == 0 {
			continue
		}
		inputCerts, err := cert.ParseCertsPEM([]byte(inputContent))
		if err != nil {
			return nil, false, fmt.Errorf("configmap/%s in %q is malformed: %v", input.Name, input.Namespace, err)
		}
		certificates = append(certificates, inputCerts...)
	}

	finalCertificates := []*x509.Certificate{}
	// now check for duplicates. n^2, but super simple
	for i := range certificates {
		found := false
		for j := range finalCertificates {
			if reflect.DeepEqual(certificates[i].Raw, finalCertificates[j].Raw) {
				found = true
				break
			}
		}
		if !found {
			finalCertificates = append(finalCertificates, certificates[i])
		}
	}
	caBytes, err := crypto.EncodeCertificates(finalCertificates...)
	if err != nil {
		return nil, false, err
	}

	requiredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespaceName, Name: "client-ca"},
		Data: map[string]string{
			"ca-bundle.crt": string(caBytes),
		},
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

// Run starts the kube-apiserver and blocks until stopCh is closed.
func (c *TargetConfigReconciler) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting TargetConfigReconciler")
	defer glog.Infof("Shutting down TargetConfigReconciler")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *TargetConfigReconciler) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *TargetConfigReconciler) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *TargetConfigReconciler) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}

// this set of namespaces will include things like logging and metrics which are used to drive
var interestingNamespaces = sets.NewString(operatorclient.TargetNamespaceName)

func (c *TargetConfigReconciler) namespaceEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				c.queue.Add(workQueueKey)
			}
			if ns.Name == operatorclient.TargetNamespaceName {
				c.queue.Add(workQueueKey)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			ns, ok := old.(*corev1.Namespace)
			if !ok {
				c.queue.Add(workQueueKey)
			}
			if ns.Name == operatorclient.TargetNamespaceName {
				c.queue.Add(workQueueKey)
			}
		},
		DeleteFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
					return
				}
				ns, ok = tombstone.Obj.(*corev1.Namespace)
				if !ok {
					utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Namespace %#v", obj))
					return
				}
			}
			if ns.Name == operatorclient.TargetNamespaceName {
				c.queue.Add(workQueueKey)
			}
		},
	}
}
