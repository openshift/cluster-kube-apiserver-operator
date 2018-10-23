package operator

import (
	"fmt"
	"reflect"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// createTargetConfigReconciler_v311_00_to_latest takes care of creation of valid resources in a fixed name.  These are inputs to other control loops.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createTargetConfigReconciler_v311_00_to_latest(c TargetConfigReconciler, operatorConfig *v1alpha1.KubeApiserverOperatorConfig) (bool, error) {
	operatorConfigOriginal := operatorConfig.DeepCopy()
	errors := []error{}

	directResourceResults := resourceapply.ApplyDirectly(c.kubeClient, v311_00_assets.Asset,
		"v3.11.0/kube-apiserver/ns.yaml",
		"v3.11.0/kube-apiserver/public-info-role.yaml",
		"v3.11.0/kube-apiserver/public-info-rolebinding.yaml",
		"v3.11.0/kube-apiserver/svc.yaml",
	)

	for _, currResult := range directResourceResults {
		if currResult.Error != nil {
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
		}
	}

	apiserverConfig, _, err := manageKubeApiserverConfigMap_v311_00_to_latest(c.kubeClient.CoreV1(), operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/deployment-kube-apiserver-config", err))
	}
	_, _, err = managePod_v311_00_to_latest(c.kubeClient.CoreV1(), operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-apiserver-pod", err))
	}

	configData := ""
	if apiserverConfig != nil {
		configData = apiserverConfig.Data["config.yaml"]
	}
	_, _, err = manageKubeApiserverPublicConfigMap_v311_00_to_latest(c.kubeClient.CoreV1(), configData, operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/public-info", err))
	}

	if len(errors) > 0 {
		message := ""
		for _, err := range errors {
			message = message + err.Error() + "\n"
		}
		v1alpha1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1alpha1.OperatorCondition{
			Type:    "TargetConfigReconiclerFailing",
			Status:  operatorv1alpha1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: message,
		})
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			_, updateError := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig)
			return true, updateError
		}
		return true, nil
	}

	v1alpha1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1alpha1.OperatorCondition{
		Type:   "TargetConfigReconiclerFailing",
		Status: operatorv1alpha1.ConditionFalse,
	})
	if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
		_, updateError := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig)
		if updateError != nil {
			return true, updateError
		}
	}

	return false, nil
}

func manageKubeApiserverConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, operatorConfig *v1alpha1.KubeApiserverOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-apiserver/defaultconfig.yaml")
	deploymentOverrides := v311_00_assets.MustAsset("v3.11.0/kube-apiserver/deployment-config-overrides.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, deploymentOverrides, operatorConfig.Spec.UserConfig.Raw, operatorConfig.Spec.ObservedConfig.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, requiredConfigMap)
}

func manageKubeApiserverPublicConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, apiserverConfigString string, operatorConfig *v1alpha1.KubeApiserverOperatorConfig) (*corev1.ConfigMap, bool, error) {
	uncastUnstructured, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(apiserverConfigString))
	if err != nil {
		return nil, false, err
	}
	apiserverConfig := uncastUnstructured.(runtime.Unstructured)

	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/public-info.yaml"))
	if operatorConfig.Status.CurrentAvailability != nil {
		configMap.Data["version"] = operatorConfig.Status.CurrentAvailability.Version
	} else {
		configMap.Data["version"] = ""
	}
	configMap.Data["imagePolicyConfig.internalRegistryHostname"], _, err = unstructured.NestedString(apiserverConfig.UnstructuredContent(), "imagePolicyConfig", "internalRegistryHostname")
	if err != nil {
		return nil, false, err
	}
	configMap.Data["imagePolicyConfig.externalRegistryHostname"], _, err = unstructured.NestedString(apiserverConfig.UnstructuredContent(), "imagePolicyConfig", "externalRegistryHostname")
	if err != nil {
		return nil, false, err
	}
	configMap.Data["projectConfig.defaultNodeSelector"], _, err = unstructured.NestedString(apiserverConfig.UnstructuredContent(), "projectConfig", "defaultNodeSelector")
	if err != nil {
		return nil, false, err
	}

	return resourceapply.ApplyConfigMap(client, configMap)
}

func managePod_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, options *v1alpha1.KubeApiserverOperatorConfig) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/pod.yaml"))
	switch corev1.PullPolicy(options.Spec.ImagePullPolicy) {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
		required.Spec.Containers[0].ImagePullPolicy = corev1.PullPolicy(options.Spec.ImagePullPolicy)
	case "":
	default:
		return nil, false, fmt.Errorf("invalid imagePullPolicy specified: %v", options.Spec.ImagePullPolicy)
	}
	required.Spec.Containers[0].Image = options.Spec.ImagePullSpec
	required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", options.Spec.Logging.Level))

	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	return resourceapply.ApplyConfigMap(client, configMap)
}
