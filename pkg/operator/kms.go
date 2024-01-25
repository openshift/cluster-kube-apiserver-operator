package operator

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type KMSAssetClass struct {
	OpenShiftInfraName string
	AWSRegion          string
}

func NewKMSAssetClass(openshiftInfraName, awsRegion string) *KMSAssetClass {
	return &KMSAssetClass{
		OpenShiftInfraName: openshiftInfraName,
		AWSRegion:          awsRegion,
	}
}

func (k *KMSAssetClass) Asset(name string) ([]byte, error) {
	b, err := bindata.Asset(name)
	if err != nil {
		return nil, err
	}

	// templated values for AWS region and OpenShift infrastructureName
	if name == "assets/kms/job-pod.yaml" {
		templatedVals := struct {
			AWSRegion        string
			OpenShiftInfraId string
		}{
			AWSRegion:        k.AWSRegion,
			OpenShiftInfraId: k.OpenShiftInfraName,
		}

		tmpl, err := template.New("kms-job").Parse(string(b))
		if err != nil {
			return nil, err
		}

		var buf bytes.Buffer
		err = tmpl.Execute(&buf, templatedVals)
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return b, nil
}

func AWSKMSKeyARNGetter(kubeClient *kubernetes.Clientset) func() string {
	return func() string {
		var err error = fmt.Errorf("to begin with")
		var cm *corev1.ConfigMap
		var keyExists bool
		var AWSKMSKeyARN string

		for err != nil && !keyExists {
			cm, err = kubeClient.CoreV1().ConfigMaps("kube-system").Get(context.TODO(), "kms-key", metav1.GetOptions{})
			AWSKMSKeyARN, keyExists = cm.Data["aws_kms_arn"]

			// retry every 5 seconds
			time.Sleep(5 * time.Second)
		}

		return AWSKMSKeyARN
	}
}
