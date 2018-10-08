package operator

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/apis/kubeapiserver/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned"
	operatorsv1alpha1client "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v311_00_assets"
)

func RunOperator(clientConfig *rest.Config, stopCh <-chan struct{}) error {
	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}

	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, targetNamespaceName, nil)

	operator := NewKubeApiserverOperator(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient.AppsV1(),
		kubeClient.CoreV1(),
		kubeClient.RbacV1(),
	)

	kubeInformersEtcdNamespaced := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, etcdNamespaceName, nil)
	configObserver := NewConfigObserver(
		operatorConfigInformers.Kubeapiserver().V1alpha1().KubeApiserverOperatorConfigs(),
		kubeInformersEtcdNamespaced,
		operatorConfigClient.KubeapiserverV1alpha1(),
		kubeClient,
	)

	ensureOperatorConfigExists(operator.operatorConfigClient, "v3.11.0/kube-apiserver/operator-config.yaml")

	operatorConfigInformers.Start(stopCh)
	kubeInformersNamespaced.Start(stopCh)

	go operator.Run(1, stopCh)
	go configObserver.Run(1, stopCh)

	<-stopCh
	return fmt.Errorf("stopped")
}

func ensureOperatorConfigExists(client operatorsv1alpha1client.KubeApiserverOperatorConfigsGetter, path string) {
	v1alpha1Scheme := runtime.NewScheme()
	v1alpha1.Install(v1alpha1Scheme)
	v1alpha1Codecs := serializer.NewCodecFactory(v1alpha1Scheme)
	operatorConfigBytes := v311_00_assets.MustAsset(path)
	operatorConfigObj, err := runtime.Decode(v1alpha1Codecs.UniversalDecoder(v1alpha1.GroupVersion), operatorConfigBytes)
	if err != nil {
		panic(err)
	}
	requiredOperatorConfig, ok := operatorConfigObj.(*v1alpha1.KubeApiserverOperatorConfig)
	if !ok {
		panic(fmt.Sprintf("unexpected object in %s: %t", path, operatorConfigObj))
	}

	hasImageEnvVar := false
	if imagePullSpecFromEnv := os.Getenv("IMAGE"); len(imagePullSpecFromEnv) > 0 {
		hasImageEnvVar = true
		requiredOperatorConfig.Spec.ImagePullSpec = imagePullSpecFromEnv
	}

	existing, err := client.KubeApiserverOperatorConfigs().Get(requiredOperatorConfig.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := client.KubeApiserverOperatorConfigs().Create(requiredOperatorConfig); err != nil {
			panic(err)
		}
		return
	}
	if err != nil {
		panic(err)
	}

	if !hasImageEnvVar {
		return
	}

	// If ImagePullSpec changed, update the existing config instance
	if existing.Spec.ImagePullSpec != requiredOperatorConfig.Spec.ImagePullSpec {
		existing.Spec.ImagePullSpec = requiredOperatorConfig.Spec.ImagePullSpec
		if _, err := client.KubeApiserverOperatorConfigs().Update(existing); err != nil {
			panic(err)
		}
	}
}
