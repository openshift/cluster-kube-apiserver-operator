package operator

import (
	"fmt"
	"reflect"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"

	corev1 "k8s.io/api/core/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	v1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
)

// createTargetConfigReconciler_v311_00_to_latest takes care of creation of valid resources in a fixed name.  These are inputs to other control loops.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createTargetConfigReconciler_v311_00_to_latest(c TargetConfigReconciler, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig) (bool, error) {
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

	// etcd is the source of truth for etcd serving CA bundles and etcd write keys.  We copy both so they can properly mounted
	_, err := manageServerEtcdCerts_v311_00_to_latest(c.kubeClient.CoreV1())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "etcd-certs", err))
	}
	apiserverConfig, _, err := manageKubeApiserverConfigMap_v311_00_to_latest(c.kubeClient.CoreV1(), operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/deployment-kube-apiserver-config", err))
	}
	_, _, err = managePod_v311_00_to_latest(c.kubeClient.CoreV1(), operatorConfig, c.targetImagePullSpec)
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
		v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
			Type:    "TargetConfigReconcilerFailing",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: message,
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

func manageServerEtcdCerts_v311_00_to_latest(client coreclientv1.CoreV1Interface) (bool, error) {
	const etcdServingCAName = "etcd-serving-ca"
	const etcdClientCertKeyPairName = "etcd-client"

	_, caChanged, err := resourceapply.SyncConfigMap(client, etcdNamespaceName, etcdServingCAName, targetNamespaceName, etcdServingCAName)
	if err != nil {
		return false, err
	}
	_, certKeyPairChanged, err := resourceapply.SyncSecret(client, etcdNamespaceName, etcdClientCertKeyPairName, targetNamespaceName, etcdClientCertKeyPairName)
	if err != nil {
		return false, err
	}
	return caChanged || certKeyPairChanged, nil
}

func manageKubeApiserverConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-apiserver/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, operatorConfig.Spec.ObservedConfig.Raw, operatorConfig.Spec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, requiredConfigMap)
}

func manageKubeApiserverPublicConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, apiserverConfigString string, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-apiserver/public-info.yaml"))

	return resourceapply.ApplyConfigMap(client, configMap)
}

func managePod_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, operatorConfig *v1alpha1.KubeAPIServerOperatorConfig, imagePullSpec string) (*corev1.ConfigMap, bool, error) {
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
	return resourceapply.ApplyConfigMap(client, configMap)
}
