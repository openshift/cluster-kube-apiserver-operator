package operator

import (
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/golang/glog"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

// createDeploymentController_v311_00_to_latest takes care of creating content for the static pods to deploy.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createDeploymentController_v311_00_to_latest(c DeploymentController, operatorConfig *v1alpha1.KubeApiserverOperatorConfig) (bool, error) {
	operatorConfigOriginal := operatorConfig.DeepCopy()
	latestDeploymentID := operatorConfig.Status.LatestDeploymentID
	isLatestDeploymentCurrent, reason := isLatestDeploymentCurrent(c.kubeClient, latestDeploymentID)

	// check to make sure that the latestDeploymentID has the exact content we expect.  No mutation here, so we start creating the next Deployment only when it is required
	if isLatestDeploymentCurrent {
		return false, nil
	}

	nextDeploymentID := latestDeploymentID + 1
	glog.Infof("new deployment %d triggered by %q", nextDeploymentID, reason)
	if err := createNewDeploymentController(c.kubeClient, nextDeploymentID); err != nil {
		v1alpha1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1alpha1.OperatorCondition{
			Type:    "DeploymentControllerFailing",
			Status:  operatorv1alpha1.ConditionTrue,
			Reason:  "ContentCreationError",
			Message: err.Error(),
		})
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			_, updateError := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig)
			return true, updateError
		}
		return true, nil
	}

	v1alpha1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1alpha1.OperatorCondition{
		Type:   "DeploymentControllerFailing",
		Status: operatorv1alpha1.ConditionFalse,
	})
	operatorConfig.Status.LatestDeploymentID = nextDeploymentID
	if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
		_, updateError := c.operatorConfigClient.KubeApiserverOperatorConfigs().UpdateStatus(operatorConfig)
		if updateError != nil {
			return true, updateError
		}
	}

	return false, nil
}

// directCopyConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller
// modifies these.
var directCopyConfigMaps = []string{
	"kube-apiserver-pod",
	"deployment-kube-apiserver-config",
	"aggregator-client-ca",
	"client-ca",
	"etcd-serving-ca",
	"kubelet-serving-ca",
	"sa-token-signing-certs",
}

// directCopySecrets is a list of secrets that are directly copied for the current values.  A different actor/controller
// modifies these.
var directCopySecrets = []string{
	"aggregator-client",
	"etcd-client",
	"kubelet-client",
	"serving-cert",
}

func nameFor(name string, deploymentID int32) string {
	return fmt.Sprintf("%s-%d", name, deploymentID)
}

// isLatestDeploymentCurrent returns whether the latest deployment is up to date and an optional reason
func isLatestDeploymentCurrent(c kubernetes.Interface, deploymentID int32) (bool, string) {
	for _, name := range directCopyConfigMaps {
		required, err := c.CoreV1().ConfigMaps(targetNamespaceName).Get(name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		existing, err := c.CoreV1().ConfigMaps(targetNamespaceName).Get(nameFor(name, deploymentID), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		if !equality.Semantic.DeepEqual(existing.Data, required.Data) {
			return false, fmt.Sprintf("configmap/%s has changed", required.Name)
		}
	}
	for _, name := range directCopySecrets {
		required, err := c.CoreV1().Secrets(targetNamespaceName).Get(name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		existing, err := c.CoreV1().Secrets(targetNamespaceName).Get(nameFor(name, deploymentID), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		if !equality.Semantic.DeepEqual(existing.Data, required.Data) {
			return false, fmt.Sprintf("secret/%s has changed", required.Name)
		}
	}

	return true, ""
}

func createNewDeploymentController(c kubernetes.Interface, deploymentID int32) error {
	for _, name := range directCopyConfigMaps {
		obj, _, err := resourceapply.SyncConfigMap(c.CoreV1(), targetNamespaceName, name, targetNamespaceName, nameFor(name, deploymentID))
		if err != nil {
			return err
		}
		if obj == nil {
			return apierrors.NewNotFound(corev1.Resource("configmaps"), name)
		}
	}
	for _, name := range directCopySecrets {
		obj, _, err := resourceapply.SyncSecret(c.CoreV1(), targetNamespaceName, name, targetNamespaceName, nameFor(name, deploymentID))
		if err != nil {
			return err
		}
		if obj == nil {
			return apierrors.NewNotFound(corev1.Resource("secrets"), name)
		}
	}

	return nil
}
