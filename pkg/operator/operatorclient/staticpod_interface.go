package operatorclient

import (
	"k8s.io/client-go/tools/cache"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/clientset/versioned/typed/kubeapiserver/v1alpha1"
	operatorclientinformers "github.com/openshift/cluster-kube-apiserver-operator/pkg/generated/informers/externalversions"
)

type StaticPodOperatorClient struct {
	Informers operatorclientinformers.SharedInformerFactory
	Client    operatorconfigclientv1alpha1.KubeapiserverV1alpha1Interface
}

func (c *StaticPodOperatorClient) Informer() cache.SharedIndexInformer {
	return c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Informer()
}

func (c *StaticPodOperatorClient) GetStaticPodOperatorState() (*operatorv1.OperatorSpec, *operatorv1.StaticPodOperatorStatus, string, error) {
	instance, err := c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return nil, nil, "", err
	}

	return &instance.Spec.OperatorSpec, &instance.Status.StaticPodOperatorStatus, instance.ResourceVersion, nil
}

func (c *StaticPodOperatorClient) UpdateStaticPodOperatorStatus(resourceVersion string, status *operatorv1.StaticPodOperatorStatus) (*operatorv1.StaticPodOperatorStatus, error) {
	original, err := c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return nil, err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = resourceVersion
	copy.Status.StaticPodOperatorStatus = *status

	ret, err := c.Client.KubeAPIServerOperatorConfigs().UpdateStatus(copy)
	if err != nil {
		return nil, err
	}

	return &ret.Status.StaticPodOperatorStatus, nil
}

func (c *StaticPodOperatorClient) GetOperatorState() (*operatorv1.OperatorSpec, *operatorv1.OperatorStatus, string, error) {
	instance, err := c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return nil, nil, "", err
	}

	return &instance.Spec.OperatorSpec, &instance.Status.StaticPodOperatorStatus.OperatorStatus, instance.ResourceVersion, nil
}

func (c *StaticPodOperatorClient) UpdateOperatorSpec(resourceVersion string, spec *operatorv1.OperatorSpec) (*operatorv1.OperatorSpec, string, error) {
	original, err := c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return nil, "", err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = resourceVersion
	copy.Spec.OperatorSpec = *spec

	ret, err := c.Client.KubeAPIServerOperatorConfigs().Update(copy)
	if err != nil {
		return nil, "", err
	}

	return &ret.Spec.OperatorSpec, ret.ResourceVersion, nil
}
func (c *StaticPodOperatorClient) UpdateOperatorStatus(resourceVersion string, status *operatorv1.OperatorStatus) (*operatorv1.OperatorStatus, error) {
	original, err := c.Informers.Kubeapiserver().V1alpha1().KubeAPIServerOperatorConfigs().Lister().Get("instance")
	if err != nil {
		return nil, err
	}
	copy := original.DeepCopy()
	copy.ResourceVersion = resourceVersion
	copy.Status.StaticPodOperatorStatus.OperatorStatus = *status

	ret, err := c.Client.KubeAPIServerOperatorConfigs().UpdateStatus(copy)
	if err != nil {
		return nil, err
	}

	return &ret.Status.StaticPodOperatorStatus.OperatorStatus, nil
}
