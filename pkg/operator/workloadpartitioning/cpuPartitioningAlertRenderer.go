package workloadpartitioning

import (
	"bytes"
	"context"
	"encoding/json"
	"text/template"

	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/cpuset"
)

// default and taken from the docs
const defaultCoresNum = 8

var performanceGroup = schema.GroupVersionResource{Group: "performance.openshift.io", Version: "v2", Resource: "performanceprofiles"}

type performanceProfileList struct {
	Items []performanceProfile `json:"items"`
}

type performanceProfile struct {
	Spec struct {
		CPU *struct {
			Reserved *string `json:"reserved"`
		} `json:"cpu"`
		NodeSelector map[string]string `json:"nodeSelector"`
	} `json:"spec"`
}

func CreateSNOAlert(ctx context.Context, client dynamic.Interface, infra v1.InfrastructureStatus, recorder events.Recorder) error {
	if infra.InfrastructureTopology != v1.SingleReplicaTopologyMode {
		return nil
	}

	cores := defaultCoresNum
	if infra.CPUPartitioning == v1.CPUPartitioningAllNodes {
		wpCores, err := workloadPartitioningCoresNum(ctx, client)
		if err != nil {
			return err
		}
		cores = wpCores
	}

	fileData, err := bindata.Asset("assets/alerts/cpu-utilization-sno.yaml")
	if err != nil {
		return err
	}
	tpl, err := template.New("tpl").Parse(string(fileData))
	if err != nil {
		return err
	}

	buf := &bytes.Buffer{}
	err = tpl.Execute(buf, cores)
	if err != nil {
		return err
	}

	alert, err := resourceread.ReadGenericWithUnstructured(buf.Bytes())
	if err != nil {
		return err
	}

	_, _, err = resourceapply.ApplyPrometheusRule(ctx, client, recorder, alert.(*unstructured.Unstructured))

	return err
}

func workloadPartitioningCoresNum(ctx context.Context, client dynamic.Interface) (int, error) {
	obj, err := client.Resource(performanceGroup).List(ctx, metav1.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return defaultCoresNum, nil
		}
		return 0, err
	}

	objRaw, err := obj.MarshalJSON()
	if err != nil {
		return 0, err
	}

	res := &performanceProfileList{}
	err = json.Unmarshal(objRaw, &res)
	if err != nil {
		return 0, err
	}
	for _, pf := range res.Items {
		if _, ok := pf.Spec.NodeSelector["node-role.kubernetes.io/master"]; !ok {
			continue
		}
		if pf.Spec.CPU == nil || pf.Spec.CPU.Reserved == nil {
			continue
		}
		return coresInCPUSet(*pf.Spec.CPU.Reserved)
	}

	return defaultCoresNum, nil
}

func coresInCPUSet(set string) (int, error) {
	cpuMap, err := cpuset.Parse(set)
	return cpuMap.Size(), err
}
