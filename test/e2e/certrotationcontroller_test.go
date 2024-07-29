package e2e

import (
	"fmt"
	"testing"
	"time"

	test "github.com/openshift/cluster-kube-apiserver-operator/test/library"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	operatorv1 "github.com/openshift/api/operator/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configexternalinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestNewCertRotationControllerHasUniqueSigners(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	configClient, err := configeversionedclient.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	operatorClient := v1helpers.NewFakeStaticPodOperatorClient(&operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}}, &operatorv1.StaticPodOperatorStatus{}, nil, nil)
	kubeAPIServerInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	)

	eventRecorder := events.NewInMemoryRecorder("")
	c, err := certrotationcontroller.NewCertRotationController(kubeClient,
		operatorClient,
		configInformers,
		kubeAPIServerInformersForNamespaces,
		eventRecorder,
		0)
	require.NoError(t, err)

	sets := map[string][]metav1.ObjectMeta{
		"Secret":    c.ControlledSecrets,
		"ConfigMap": c.ControlledConfigMaps,
	}
	for objType, set := range sets {
		slice := make(map[string]bool)
		for _, objMeta := range set {
			objKey := fmt.Sprintf("%s/%s", objMeta.Name, objMeta.Namespace)
			if _, found := slice[objKey]; !found {
				slice[objKey] = true
			} else {
				t.Fatalf("%s %s is being managed by two controllers", objType, objKey)
			}
		}
	}
}
